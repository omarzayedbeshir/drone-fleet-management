// Package adaptertest is a reusable conformance suite for vendor adapters
// (NFR-07, NFR-09: "every vendor adapter is testable against a mock vendor
// feed"). Any adapter, including Seif's connector-manager tests and the
// MAVLink adapter, can call Run from its own _test.go to prove it follows the
// VendorAdapter contract.
//
// This suite checks the contract of the ADAPTER. The contract of the
// NORMALIZED output (common schema) is Ali's shared contract tests.
package adaptertest

import (
	"context"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

// Options tunes Run.
type Options struct {
	// MinRecords is how many records to collect before cancelling. Default 5.
	MinRecords int
	// WantVendor, if set, is the value every record's Vendor must equal.
	// Default: the adapter's Name().
	WantVendor string
	// WantSimulated asserts the Simulated flag on every record.
	WantSimulated bool
	// Timeout bounds the whole run. Default 10s.
	Timeout time.Duration
}

// Run executes the conformance checks against a freshly built adapter.
func Run(t *testing.T, build func(t *testing.T) vendor.VendorAdapter, opt Options) {
	t.Helper()
	if opt.MinRecords == 0 {
		opt.MinRecords = 5
	}
	if opt.Timeout == 0 {
		opt.Timeout = 10 * time.Second
	}

	a := build(t)
	if a.Name() == "" {
		t.Fatal("Name() must not be empty")
	}
	wantVendor := opt.WantVendor
	if wantVendor == "" {
		wantVendor = a.Name()
	}
	if got := a.Health().State; got != vendor.HealthUnknown {
		t.Errorf("health before Connect = %q, want %q", got, vendor.HealthUnknown)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opt.Timeout)
	defer cancel()

	if err := a.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	subCtx, stop := context.WithCancel(ctx)
	defer stop()
	out := make(chan vendor.RawRecord)
	done := make(chan error, 1)
	go func() { done <- a.Subscribe(subCtx, out) }()

	var recs []vendor.RawRecord
	finished := false
	var subErr error
	for len(recs) < opt.MinRecords && !finished {
		select {
		case r := <-out:
			recs = append(recs, r)
		case err := <-done:
			finished, subErr = true, err
		case <-ctx.Done():
			t.Fatalf("timed out after %d/%d records", len(recs), opt.MinRecords)
		}
	}
	stop()
	if !finished {
		select {
		case subErr = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Subscribe did not return within 2s of context cancellation")
		}
	}
	if len(recs) < opt.MinRecords {
		t.Fatalf("got %d records, want at least %d (Subscribe error: %v)", len(recs), opt.MinRecords, subErr)
	}

	for i, r := range recs {
		if r.Vendor != wantVendor {
			t.Errorf("record %d: Vendor = %q, want %q", i, r.Vendor, wantVendor)
		}
		if r.SourceDroneID == "" {
			t.Errorf("record %d: empty SourceDroneID", i)
		}
		if r.ReceivedAt.IsZero() || r.ReceivedAt.Location() != time.UTC {
			t.Errorf("record %d: ReceivedAt must be set and in UTC, got %v", i, r.ReceivedAt)
		}
		if opt.WantSimulated && !r.Simulated {
			t.Errorf("record %d: Simulated = false, want true", i)
		}
		for k, v := range r.Payload {
			switch v.(type) {
			case map[string]any, []any:
				t.Errorf("record %d: payload key %q is nested; adapters must flatten (rawutil.Flatten)", i, k)
			}
		}
	}

	h := a.Health()
	if h.State != vendor.HealthHealthy && h.State != vendor.HealthDegraded {
		t.Errorf("health after streaming = %q, want healthy or degraded", h.State)
	}
	if h.LastContact.IsZero() {
		t.Error("LastContact must be set after records were received")
	}

	if err := a.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("second Close must be a no-op, got %v", err)
	}
}
