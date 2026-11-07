// Package ingestion bridges raw vendor sources to the application's normalized
// adapter contract. No raw payload is buffered before the privacy allowlist.
package ingestion

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/buffer"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/normalize"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/privacy"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

const permanentSourceFailure = "Permanent source configuration failure"

type Config struct {
	Source             vendor.VendorAdapter
	Serial             string
	Vendor             string
	Simulated          bool
	Capacity           int
	RetryMin, RetryMax time.Duration
}
type Adapter struct {
	cfg    Config
	mu     sync.Mutex
	health adapters.Health
	// Protected by mu; terminal source failure takes priority over buffered writes.
	permanentFailure bool
	running          atomic.Bool
}

var _ adapters.VendorAdapter = (*Adapter)(nil)

func New(c Config) (*Adapter, error) {
	if c.Source == nil || c.Serial == "" || (c.Vendor != "dji" && c.Vendor != "ardupilot") {
		return nil, errors.New("source, serial and supported vendor required")
	}
	if c.Capacity == 0 {
		c.Capacity = 32
	}
	if c.Capacity < 1 || c.Capacity > 4096 {
		return nil, errors.New("invalid queue capacity")
	}
	if c.RetryMin == 0 {
		c.RetryMin = 250 * time.Millisecond
	}
	if c.RetryMax == 0 {
		c.RetryMax = 10 * time.Second
	}
	if c.RetryMin < 0 || c.RetryMax < c.RetryMin {
		return nil, errors.New("invalid retry timing")
	}
	return &Adapter{cfg: c}, nil
}
func (a *Adapter) change(f func(*adapters.Health)) { a.mu.Lock(); defer a.mu.Unlock(); f(&a.health) }
func (a *Adapter) Health() adapters.Health {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.health
	if a.permanentFailure {
		h.LastError = permanentSourceFailure
	}
	h.Connected = a.running.Load() && h.Connected
	h.Ready = h.Connected && !h.LastEmitted.IsZero() && time.Since(h.LastEmitted) < 10*time.Second
	return h
}
func (a *Adapter) Run(parent context.Context, emit adapters.Emit) error {
	if emit == nil {
		return errors.New("ingestion callback required")
	}
	if !a.running.CompareAndSwap(false, true) {
		return errors.New("adapter already running")
	}
	defer a.running.Store(false)
	a.change(func(h *adapters.Health) { a.permanentFailure = false; h.LastError = "" })
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	q, _ := buffer.New(a.cfg.Capacity)
	producerDone := make(chan struct{})
	var producerErr error
	go func() { defer close(producerDone); defer q.Close(); producerErr = a.produce(ctx, q) }()
	defer func() { cancel(); <-producerDone }()
	normal := normalize.New()
	for {
		r, err := q.Dequeue(ctx)
		if errors.Is(err, buffer.ErrClosed) {
			<-producerDone
			return producerErr
		}
		if err != nil {
			return nil
		}
		sample, err := normal.Normalize(r)
		if r["_simulated"] == true {
			sample.Source = "csv"
		}
		if err != nil {
			a.reject()
			continue
		}
		delay := a.cfg.RetryMin
		for {
			err = emit(ctx, sample)
			if err == nil {
				a.change(func(h *adapters.Health) {
					h.Accepted++
					h.LastEmitted = time.Now()
					h.LastError = ""
				})
				break
			}
			if ctx.Err() != nil {
				return nil
			}
			var permanent *adapters.PermanentError
			var rejection *telemetry.Rejection
			if errors.As(err, &permanent) || errors.As(err, &rejection) || errors.Is(err, telemetry.ErrNotFound) {
				a.reject()
				break
			}
			a.change(func(h *adapters.Health) { h.LastError = "Ingestion unavailable; retrying buffered observation" })
			if !wait(ctx, delay) {
				return nil
			}
			delay = min(delay*2, a.cfg.RetryMax)
		}
	}
}
func (a *Adapter) reject() {
	a.change(func(h *adapters.Health) { h.Rejected++; h.LastError = "Invalid telemetry rejected" })
	slog.Warn("Invalid telemetry rejected", "vendor", a.cfg.Vendor)
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (a *Adapter) produce(ctx context.Context, q *buffer.Buffer) error {
	delay := a.cfg.RetryMin
	var lastFull time.Time
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := a.cfg.Source.Connect(ctx)
		if err == nil {
			a.change(func(h *adapters.Health) { h.Connected = true })
			out := make(chan vendor.RawRecord)
			done := make(chan error, 1)
			subctx, stop := context.WithCancel(ctx)
			go func() { defer close(out); done <- a.cfg.Source.Subscribe(subctx, out) }()
			for raw := range out {
				if raw.SourceDroneID != a.cfg.Serial {
					continue
				}
				clean, e := privacy.NewFilter().Process(ctx, telemetry.RawRecord(raw.Payload))
				if e != nil {
					continue
				}
				clean["source"] = a.cfg.Vendor
				clean["_simulated"] = raw.Simulated || a.cfg.Simulated
				clean["device_sn"] = raw.SourceDroneID
				a.change(func(h *adapters.Health) { h.LastFrame = time.Now() })
				if q.Len() == q.Capacity() && time.Since(lastFull) > 10*time.Second {
					lastFull = time.Now()
					slog.Warn("Telemetry buffer full; applying backpressure", "vendor", a.cfg.Vendor)
				}
				if e = q.Enqueue(ctx, clean); e != nil {
					stop()
					for range out {
					}
					break
				}
			}
			stop()
			err = <-done
		}
		_ = a.cfg.Source.Close()
		a.change(func(h *adapters.Health) { h.Connected = false })
		if err == nil || ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, vendor.ErrPermanent) {
			a.change(func(h *adapters.Health) { a.permanentFailure = true; h.LastError = permanentSourceFailure })
			slog.Error("Permanent telemetry source failure", "vendor", a.cfg.Vendor)
			return err
		}
		a.change(func(h *adapters.Health) { h.LastError = "Vendor unavailable; reconnecting" })
		slog.Warn("Vendor unavailable; retrying", "vendor", a.cfg.Vendor)
		if !wait(ctx, delay) {
			return nil
		}
		delay = min(delay*2, a.cfg.RetryMax)
	}
}
