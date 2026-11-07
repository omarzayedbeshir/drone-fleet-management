package ingestion

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

type scriptedSource struct {
	mu          sync.Mutex
	connectErrs []error
	alwaysFail  error
	sessions    [][]vendor.RawRecord
	sessionErrs []error
	block       bool
	connects    []time.Time
	closes      int
}

func (s *scriptedSource) Name() string { return "scripted" }

func (s *scriptedSource) Connect(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connects = append(s.connects, time.Now())
	if s.alwaysFail != nil {
		return s.alwaysFail
	}
	if len(s.connectErrs) > 0 {
		err := s.connectErrs[0]
		s.connectErrs = s.connectErrs[1:]
		return err
	}
	return nil
}

func (s *scriptedSource) Subscribe(ctx context.Context, out chan<- vendor.RawRecord) error {
	s.mu.Lock()
	var recs []vendor.RawRecord
	if len(s.sessions) > 0 {
		recs, s.sessions = s.sessions[0], s.sessions[1:]
	}
	var end error
	hasEnd := len(s.sessionErrs) > 0
	if hasEnd {
		end, s.sessionErrs = s.sessionErrs[0], s.sessionErrs[1:]
	}
	block := s.block
	s.mu.Unlock()
	for _, r := range recs {
		select {
		case out <- r:
		case <-ctx.Done():
			return nil
		}
	}
	if hasEnd {
		return end
	}
	if block {
		<-ctx.Done()
	}
	return nil
}

func (s *scriptedSource) Close() error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return nil
}

func (s *scriptedSource) Health() vendor.Health { return vendor.Health{} }

func (s *scriptedSource) connectTimes() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.connects...)
}

func (s *scriptedSource) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}

var base = time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)

func raw(serial string, seq int, simulated bool) vendor.RawRecord {
	return vendor.RawRecord{
		Vendor:        "dji",
		SourceDroneID: serial,
		Simulated:     simulated,
		Payload: map[string]any{
			"device_sn":                serial,
			"timestamp":                base.Add(time.Duration(seq) * time.Second).Format(time.RFC3339),
			"latitude":                 30.1,
			"longitude":                31.2,
			"height":                   50.0,
			"battery_capacity_percent": 80.0,
			"total_flight_time":        int64(3600 + seq),
			"battery_loop_times":       int64(20),
		},
	}
}

func newAdapter(t *testing.T, src vendor.VendorAdapter, min, max time.Duration) *Adapter {
	t.Helper()
	a, err := New(Config{Source: src, Serial: "D1", Vendor: "dji", RetryMin: min, RetryMax: max})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func gaps(ts []time.Time) []time.Duration {
	out := make([]time.Duration, 0, len(ts))
	for i := 1; i < len(ts); i++ {
		out = append(out, ts[i].Sub(ts[i-1]))
	}
	return out
}

func checkGaps(t *testing.T, got []time.Duration, want ...time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d gaps %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] < want[i]*9/10 || got[i] > want[i]+250*time.Millisecond {
			t.Errorf("gap %d = %v, want about %v (all gaps %v)", i, got[i], want[i], got)
		}
	}
}

func accept(context.Context, telemetry.Sample) error { return nil }

func TestNewRejectsInvalidConfig(t *testing.T) {
	src := &scriptedSource{}
	cases := []struct {
		name string
		cfg  Config
	}{
		{"nil source", Config{Serial: "D1", Vendor: "dji"}},
		{"empty serial", Config{Source: src, Vendor: "dji"}},
		{"unsupported vendor", Config{Source: src, Serial: "D1", Vendor: "parrot"}},
		{"capacity above limit", Config{Source: src, Serial: "D1", Vendor: "dji", Capacity: 4097}},
		{"negative capacity", Config{Source: src, Serial: "D1", Vendor: "dji", Capacity: -1}},
		{"negative retry", Config{Source: src, Serial: "D1", Vendor: "dji", RetryMin: -time.Second}},
		{"max below min", Config{Source: src, Serial: "D1", Vendor: "dji", RetryMin: time.Second, RetryMax: time.Millisecond}},
	}
	for _, c := range cases {
		if _, err := New(c.cfg); err == nil {
			t.Errorf("%s: New accepted invalid config", c.name)
		}
	}
}

func TestNewAppliesDefaultsAndBoundaries(t *testing.T) {
	src := &scriptedSource{}
	a, err := New(Config{Source: src, Serial: "D1", Vendor: "ardupilot"})
	if err != nil {
		t.Fatal(err)
	}
	if a.cfg.Capacity != 32 || a.cfg.RetryMin != 250*time.Millisecond || a.cfg.RetryMax != 10*time.Second {
		t.Fatalf("defaults = capacity %d, retry %v..%v", a.cfg.Capacity, a.cfg.RetryMin, a.cfg.RetryMax)
	}
	for _, capacity := range []int{1, 4096} {
		if _, err := New(Config{Source: src, Serial: "D1", Vendor: "dji", Capacity: capacity}); err != nil {
			t.Errorf("capacity %d rejected: %v", capacity, err)
		}
	}
	if _, err := New(Config{Source: src, Serial: "D1", Vendor: "dji", RetryMin: time.Second, RetryMax: time.Second}); err != nil {
		t.Errorf("RetryMin == RetryMax rejected: %v", err)
	}
}

func TestRunRequiresCallbackAndSingleRun(t *testing.T) {
	src := &scriptedSource{block: true}
	a := newAdapter(t, src, time.Millisecond, time.Millisecond)
	if err := a.Run(context.Background(), nil); err == nil {
		t.Fatal("Run accepted a nil callback")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, accept) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(src.connectTimes()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := a.Run(context.Background(), accept); err == nil {
		t.Error("second concurrent Run was accepted")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("first Run returned %v", err)
	}
}

func TestReconnectDelayDoublesUntilCap(t *testing.T) {
	temp := errors.New("vendor unreachable")
	src := &scriptedSource{connectErrs: []error{temp, temp, temp, temp, temp}}
	a := newAdapter(t, src, 20*time.Millisecond, 80*time.Millisecond)
	if err := a.Run(context.Background(), accept); err != nil {
		t.Fatalf("Run = %v", err)
	}
	checkGaps(t, gaps(src.connectTimes()), 20*time.Millisecond, 40*time.Millisecond, 80*time.Millisecond, 80*time.Millisecond, 80*time.Millisecond)
	if src.closeCount() != 6 {
		t.Errorf("Close called %d times, want 6 (once per attempt)", src.closeCount())
	}
}

func TestWrappedPermanentSessionErrorStopsRetrying(t *testing.T) {
	src := &scriptedSource{
		sessions:    [][]vendor.RawRecord{{raw("D1", 1, false)}},
		sessionErrs: []error{fmt.Errorf("token revoked: %w", vendor.ErrPermanent)},
	}
	a := newAdapter(t, src, time.Millisecond, time.Millisecond)
	accepted := 0
	err := a.Run(context.Background(), func(context.Context, telemetry.Sample) error { accepted++; return nil })
	if !errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("Run = %v, want ErrPermanent", err)
	}
	if n := len(src.connectTimes()); n != 1 {
		t.Errorf("Connect called %d times, want 1", n)
	}
	if h := a.Health(); h.LastError != "Permanent source configuration failure" || h.Connected || accepted != 1 {
		t.Errorf("health %+v accepted %d", h, accepted)
	}
}

func TestFinishedSourceIsNotReconnected(t *testing.T) {
	src := &scriptedSource{sessions: [][]vendor.RawRecord{{raw("D1", 1, false), raw("D1", 2, false)}}}
	a := newAdapter(t, src, time.Millisecond, time.Millisecond)
	accepted := 0
	if err := a.Run(context.Background(), func(context.Context, telemetry.Sample) error { accepted++; return nil }); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if n := len(src.connectTimes()); n != 1 || src.closeCount() != 1 || accepted != 2 {
		t.Errorf("connects %d closes %d accepted %d, want 1 1 2", n, src.closeCount(), accepted)
	}
}

func TestOnlyConfiguredSerialIsEmitted(t *testing.T) {
	src := &scriptedSource{sessions: [][]vendor.RawRecord{{raw("D1", 1, false), raw("D2", 2, false), raw("D1", 3, false), raw("D3", 4, false)}}}
	a := newAdapter(t, src, time.Millisecond, time.Millisecond)
	var got []telemetry.Sample
	if err := a.Run(context.Background(), func(_ context.Context, s telemetry.Sample) error { got = append(got, s); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || a.Health().Accepted != 2 {
		t.Fatalf("emitted %d samples (accepted %d), want 2", len(got), a.Health().Accepted)
	}
}

func TestSimulatedFlagLabelsSampleSource(t *testing.T) {
	for _, simulated := range []bool{false, true} {
		src := &scriptedSource{sessions: [][]vendor.RawRecord{{raw("D1", 1, simulated)}}}
		a := newAdapter(t, src, time.Millisecond, time.Millisecond)
		var got telemetry.Sample
		if err := a.Run(context.Background(), func(_ context.Context, s telemetry.Sample) error { got = s; return nil }); err != nil {
			t.Fatal(err)
		}
		want := "dji"
		if simulated {
			want = "csv"
		}
		if got.Source != want {
			t.Errorf("simulated=%v: Source = %q, want %q", simulated, got.Source, want)
		}
	}
}

func TestHealthTracksLiveSession(t *testing.T) {
	src := &scriptedSource{sessions: [][]vendor.RawRecord{{raw("D1", 1, false)}}, block: true}
	a := newAdapter(t, src, time.Millisecond, time.Millisecond)
	if h := a.Health(); h.Connected || h.Ready {
		t.Fatalf("health before Run = %+v", h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, accept) }()
	deadline := time.Now().Add(2 * time.Second)
	for a.Health().Accepted == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	h := a.Health()
	if !h.Connected || !h.Ready || h.LastFrame.IsZero() || h.LastEmitted.IsZero() || h.LastError != "" {
		t.Errorf("health during session = %+v", h)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if h := a.Health(); h.Connected || h.Ready {
		t.Errorf("health after Run = %+v", h)
	}
}

func TestCancelDuringReconnectBackoffReturnsPromptly(t *testing.T) {
	src := &scriptedSource{alwaysFail: errors.New("vendor unreachable")}
	a := newAdapter(t, src, time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, accept) }()
	deadline := time.Now().Add(2 * time.Second)
	for len(src.connectTimes()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	if h := a.Health(); h.LastError != "Vendor unavailable; reconnecting" || h.Connected {
		t.Errorf("health while backing off = %+v", h)
	}
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
		if waited := time.Since(start); waited > time.Second {
			t.Errorf("Run took %v to stop", waited)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop during backoff")
	}
}

func TestEmitRetryDelayDoublesUntilCap(t *testing.T) {
	src := &scriptedSource{sessions: [][]vendor.RawRecord{{raw("D1", 1, false)}}}
	a := newAdapter(t, src, 20*time.Millisecond, 40*time.Millisecond)
	var attempts []time.Time
	err := a.Run(context.Background(), func(context.Context, telemetry.Sample) error {
		attempts = append(attempts, time.Now())
		if len(attempts) <= 4 {
			return errors.New("store unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkGaps(t, gaps(attempts), 20*time.Millisecond, 40*time.Millisecond, 40*time.Millisecond, 40*time.Millisecond)
	if h := a.Health(); h.Accepted != 1 || h.LastError != "" {
		t.Errorf("health = %+v", h)
	}
}

func TestPermanentEmitErrorRejectsAndContinues(t *testing.T) {
	src := &scriptedSource{sessions: [][]vendor.RawRecord{{raw("D1", 1, false), raw("D1", 2, false)}}}
	a := newAdapter(t, src, time.Millisecond, time.Millisecond)
	calls := 0
	err := a.Run(context.Background(), func(context.Context, telemetry.Sample) error {
		calls++
		if calls == 1 {
			return &adapters.PermanentError{Err: errors.New("drone not registered")}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if h := a.Health(); calls != 2 || h.Rejected != 1 || h.Accepted != 1 {
		t.Errorf("calls %d health %+v, want 2 calls, 1 rejected, 1 accepted", calls, h)
	}
}

func TestCancelDuringEmitRetryReturnsPromptly(t *testing.T) {
	src := &scriptedSource{sessions: [][]vendor.RawRecord{{raw("D1", 1, false)}}, block: true}
	a := newAdapter(t, src, time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx, func(context.Context, telemetry.Sample) error {
			select {
			case called <- struct{}{}:
			default:
			}
			return errors.New("store unavailable")
		})
	}()
	<-called
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop while retrying emit")
	}
}

// A buffered sample may still be processed after the producer has terminated.
// Synchronize on the terminal failure so this regression does not depend on
// which goroutine happens to finish first.
func TestPermanentSourceFailureSurvivesBufferedStoreErrors(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "temporary store error then success"
		if reject {
			name = "rejected buffered sample"
		}
		t.Run(name, func(t *testing.T) {
			src := &scriptedSource{
				sessions:    [][]vendor.RawRecord{{raw("D1", 1, false)}, {raw("D1", 2, false)}},
				sessionErrs: []error{fmt.Errorf("credentials revoked: %w", vendor.ErrPermanent)},
			}
			a := newAdapter(t, src, time.Millisecond, time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			attempts := 0
			err := a.Run(ctx, func(context.Context, telemetry.Sample) error {
				attempts++
				if attempts == 1 {
					for a.Health().LastError != permanentSourceFailure {
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(time.Millisecond):
						}
					}
					if reject {
						return &adapters.PermanentError{Err: errors.New("invalid sample")}
					}
					return errors.New("temporary database outage")
				}
				return nil
			})
			if !errors.Is(err, vendor.ErrPermanent) {
				t.Fatalf("Run = %v, want source failure", err)
			}
			h := a.Health()
			if h.LastError != permanentSourceFailure || h.Ready || h.Connected {
				t.Fatalf("terminal source failure lost: %+v", h)
			}
			if reject {
				if attempts != 1 || h.Rejected != 1 || h.Accepted != 0 {
					t.Fatalf("attempts %d health %+v", attempts, h)
				}
			} else if attempts != 2 || h.Accepted != 1 {
				t.Fatalf("attempts %d health %+v", attempts, h)
			}
			// Explicitly starting the same adapter again must clear the old failure.
			if err := a.Run(ctx, accept); err != nil {
				t.Fatal(err)
			}
			if h := a.Health(); h.LastError != "" {
				t.Fatalf("stale terminal failure after new run: %+v", h)
			}
		})
	}
}
