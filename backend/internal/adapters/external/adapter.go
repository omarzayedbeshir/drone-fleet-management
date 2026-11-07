// Package external runs a trusted operator-installed adapter module. Its stdout
// protocol is bounded NDJSON containing only the existing normalized schema.
package external

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/catalog"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

// Binding is sent once on stdin. It carries no database URL, user credentials,
// arbitrary SQL identity or write access; the host owns tenant-scoped persistence.
type Binding struct {
	Protocol       string `json:"protocol"`
	Vendor         string `json:"vendor"`
	Serial         string `json:"serial"`
	FlightBaseline int64  `json:"flight_time_seconds"`
	CycleBaseline  int64  `json:"battery_cycles"`
}
type Config struct {
	Executable                    string
	Arguments                     []string
	Vendor, Serial, CredentialRef string
	FlightBaseline, CycleBaseline int64
	QueueSize                     int
}
type Adapter struct {
	cfg     Config
	mu      sync.Mutex
	health  adapters.Health
	running atomic.Bool
}

var _ adapters.VendorAdapter = (*Adapter)(nil)

func New(c Config) (*Adapter, error) {
	if c.Executable == "" || !catalog.ValidVendor(c.Vendor) || c.Serial == "" || c.FlightBaseline < 0 || c.CycleBaseline < 0 || c.QueueSize < 0 || c.QueueSize > 4096 {
		return nil, errors.New("invalid external adapter configuration")
	}
	path, err := exec.LookPath(c.Executable)
	if err != nil {
		return nil, errors.New("adapter module executable is unavailable")
	}
	c.Executable = path
	if c.QueueSize == 0 {
		c.QueueSize = 32
	}
	return &Adapter{cfg: c}, nil
}
func (a *Adapter) change(f func(*adapters.Health)) { a.mu.Lock(); defer a.mu.Unlock(); f(&a.health) }
func (a *Adapter) Health() adapters.Health {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.health
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
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	input, _ := json.Marshal(Binding{"aerosync.telemetry.v1", a.cfg.Vendor, a.cfg.Serial, a.cfg.FlightBaseline, a.cfg.CycleBaseline})
	cmd := exec.CommandContext(ctx, a.cfg.Executable, a.cfg.Arguments...)
	configureProcess(cmd)
	cmd.Stdin = bytes.NewReader(append(input, '\n'))
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	// Deliberately do not inherit DATABASE_URL, session secrets or unrelated vendor
	// accounts. The module receives only its explicitly referenced credential set.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "PYTHONUNBUFFERED=1"}
	if a.cfg.CredentialRef != "" {
		prefix := "AEROSYNC_" + strings.ToUpper(a.cfg.CredentialRef) + "_"
		for _, entry := range os.Environ() {
			if strings.HasPrefix(entry, prefix) {
				cmd.Env = append(cmd.Env, entry)
			}
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.New("cannot open module output")
	}
	if err = cmd.Start(); err != nil {
		return errors.New("cannot start adapter module")
	}
	a.change(func(h *adapters.Health) { h.Connected = true; h.LastError = "" })
	queue := make(chan telemetry.Sample, a.cfg.QueueSize)
	readerDone := make(chan error, 1)
	go func() { defer close(queue); readerDone <- a.read(ctx, stdout, queue) }()
	waited := false
	wait := func() error {
		if waited {
			return nil
		}
		waited = true
		return cmd.Wait()
	}
	defer func() {
		cancel()
		_ = stdout.Close()
		<-readerDone
		_ = wait()
		a.change(func(h *adapters.Health) { h.Connected = false })
	}()
	// Closing the pipe makes cancellation bounded even when a child inherited it.
	pipeDone := make(chan struct{})
	defer close(pipeDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = stdout.Close()
		case <-pipeDone:
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case sample, ok := <-queue:
			if !ok {
				err = wait()
				if ctx.Err() != nil {
					return nil
				}
				if err != nil {
					return errors.New("adapter module exited unsuccessfully")
				}
				return nil
			}
			delay := 250 * time.Millisecond
			for {
				err = emit(ctx, sample)
				if err == nil {
					a.change(func(h *adapters.Health) { h.Accepted++; h.LastEmitted = time.Now(); h.LastError = "" })
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
				t := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					t.Stop()
					return nil
				case <-t.C:
				}
				delay = min(delay*2, 10*time.Second)
			}
		}
	}
}
func (a *Adapter) reject() {
	slog.Warn("External adapter record rejected", "vendor", a.cfg.Vendor)
	a.change(func(h *adapters.Health) { h.Rejected++; h.LastError = "Module record rejected by the common schema" })
}
func (a *Adapter) read(ctx context.Context, stream io.Reader, out chan<- telemetry.Sample) error {
	reader := bufio.NewReaderSize(stream, telemetry.MaxBody+1)
	oversize := false
	var lastFull time.Time
	for {
		raw, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			oversize = true
			continue
		}
		if oversize || len(raw) > telemetry.MaxBody {
			a.reject()
			oversize = false
		} else if len(bytes.TrimSpace(raw)) > 0 {
			a.change(func(h *adapters.Health) { h.LastFrame = time.Now() })
			sample, e := telemetry.Decode(raw, time.Now())
			if e != nil || sample.Source != a.cfg.Vendor {
				a.reject()
			} else {
				if len(out) == cap(out) && time.Since(lastFull) > 10*time.Second {
					lastFull = time.Now()
					slog.Warn("External adapter queue full; applying backpressure", "vendor", a.cfg.Vendor)
				}
				select {
				case out <- sample:
				case <-ctx.Done():
					return nil
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
