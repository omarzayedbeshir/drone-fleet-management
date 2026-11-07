// Package mockfeed is the mock vendor feed (NFR-09, R-06, R-09). It produces
// DJI-style telemetry and can inject the three failure modes the ingestion
// side has to survive:
//
//   - slow:      every call/record is delayed by Faults.Latency
//   - down:      the vendor refuses connections / returns errors
//   - malformed: a fraction of records are corrupted in a chosen way
//
// It is used two ways:
//
//  1. In-process, as a vendor.VendorAdapter (Feed). Ali and Ziad can test the
//     privacy filter, buffer, normalizer, validator and writer with it before
//     any real adapter works.
//  2. Over HTTP (Server / cmd/mockfeed), imitating a vendor cloud API so the
//     DJI adapter and the connector manager can be tested end to end, and so a
//     live demo can switch failures on and off at runtime.
//
// Everything it emits is marked Simulated.
package mockfeed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/internal/rawutil"
	"github.com/omarzayedbeshir/drone-fleet-management/internal/vendor"
)

// ErrVendorDown is returned while Faults.Down is set.
var ErrVendorDown = errors.New("mockfeed: vendor is down (injected fault)")

// Faults is the set of failures currently being injected. Safe to change at
// any time through Feed.SetFaults.
type Faults struct {
	Down           bool
	Latency        time.Duration
	MalformedRate  float64         // 0..1 probability that a record is corrupted
	MalformedKinds []MalformedKind // restrict the kinds; empty = all applicable
}

// Config configures a Feed.
type Config struct {
	Name           string           // adapter name, default "mock"
	DroneIDs       []string         // default: MOCK-0001..MOCK-0003
	Interval       time.Duration    // pause between rounds; 0 = as fast as possible
	MaxRecords     int              // stop (return nil) after this many records; 0 = unlimited
	Seed           int64            // default 1
	Now            func() time.Time // default time.Now
	IncludeImagery bool             // add camera/payload fields for privacy-filter tests
	Faults         Faults           // initial faults
}

// Feed implements vendor.VendorAdapter on top of the generator.
type Feed struct {
	cfg Config
	gen *generator

	mu     sync.RWMutex
	faults Faults
	health vendor.Health
}

// New builds a Feed.
func New(cfg Config) *Feed {
	if cfg.Name == "" {
		cfg.Name = "mock"
	}
	if len(cfg.DroneIDs) == 0 {
		cfg.DroneIDs = []string{"MOCK-0001", "MOCK-0002", "MOCK-0003"}
	}
	if cfg.Seed == 0 {
		cfg.Seed = 1
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Feed{
		cfg:    cfg,
		gen:    newGenerator(cfg.DroneIDs, cfg.Seed, cfg.Now, cfg.IncludeImagery),
		faults: cfg.Faults,
		health: vendor.Health{State: vendor.HealthUnknown},
	}
}

// SetFaults replaces the injected faults.
func (f *Feed) SetFaults(faults Faults) {
	f.mu.Lock()
	f.faults = faults
	f.mu.Unlock()
}

// Faults returns the faults currently injected.
func (f *Feed) Faults() Faults {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.faults
}

// Devices returns the drone ids this feed simulates.
func (f *Feed) Devices() []string { return append([]string(nil), f.cfg.DroneIDs...) }

func (f *Feed) setHealth(state vendor.HealthState, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.health.State = state
	if err != nil {
		f.health.LastError = err.Error()
	} else {
		f.health.LastError = ""
		f.health.LastContact = time.Now().UTC()
	}
}

// ---- vendor.VendorAdapter ----

// Name implements vendor.VendorAdapter.
func (f *Feed) Name() string { return f.cfg.Name }

// Connect implements vendor.VendorAdapter.
func (f *Feed) Connect(ctx context.Context) error {
	faults := f.Faults()
	if err := sleep(ctx, faults.Latency); err != nil {
		return err
	}
	if faults.Down {
		f.setHealth(vendor.HealthDown, ErrVendorDown)
		return ErrVendorDown
	}
	f.setHealth(vendor.HealthHealthy, nil)
	return nil
}

// Subscribe implements vendor.VendorAdapter.
func (f *Feed) Subscribe(ctx context.Context, out chan<- vendor.RawRecord) error {
	emitted := 0
	for {
		for _, id := range f.cfg.DroneIDs {
			faults := f.Faults()
			if faults.Down {
				f.setHealth(vendor.HealthDown, ErrVendorDown)
				return ErrVendorDown
			}
			if err := sleep(ctx, faults.Latency); err != nil {
				return err
			}
			native, _ := f.gen.next(id, faults.MalformedRate, intersect(adapterKinds, faults.MalformedKinds))
			rec := vendor.RawRecord{
				Vendor:        f.cfg.Name,
				SourceDroneID: id,
				ReceivedAt:    f.cfg.Now().UTC(),
				Simulated:     true,
				Payload:       rawutil.Flatten(native),
			}
			select {
			case out <- rec:
			case <-ctx.Done():
				return ctx.Err()
			}
			state := vendor.HealthHealthy
			if faults.Latency > 0 {
				state = vendor.HealthDegraded
			}
			f.setHealth(state, nil)
			emitted++
			if f.cfg.MaxRecords > 0 && emitted >= f.cfg.MaxRecords {
				return nil
			}
		}
		if err := sleep(ctx, f.cfg.Interval); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// Health implements vendor.VendorAdapter.
func (f *Feed) Health() vendor.Health {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.health
}

// Close implements vendor.VendorAdapter.
func (f *Feed) Close() error { return nil }

var _ vendor.VendorAdapter = (*Feed)(nil)

// sleep waits for d or until ctx is cancelled. d <= 0 returns immediately.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("mockfeed: %w", ctx.Err())
	}
}
