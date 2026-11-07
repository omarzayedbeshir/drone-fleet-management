package dji

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/adaptertest"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/credentials"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/mockfeed"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

const token = "test-token"

func creds(tok string) credentials.Provider {
	return credentials.StaticProvider{"dji": {"api_key": tok}}
}

func startMock(t *testing.T, feed *mockfeed.Feed) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(mockfeed.NewServer(feed, mockfeed.ServerConfig{Token: token}))
	t.Cleanup(s.Close)
	return s
}

func newAdapter(t *testing.T, url string, mutate func(*Config)) *Adapter {
	t.Helper()
	cfg := Config{
		BaseURL:                url,
		Credentials:            creds(token),
		PollInterval:           5 * time.Millisecond,
		RequestTimeout:         500 * time.Millisecond,
		MaxConsecutiveFailures: 2,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestConformanceAgainstMockVendor(t *testing.T) {
	s := startMock(t, mockfeed.New(mockfeed.Config{DroneIDs: []string{"D1", "D2"}}))
	adaptertest.Run(t, func(t *testing.T) vendor.VendorAdapter {
		return newAdapter(t, s.URL, nil)
	}, adaptertest.Options{WantVendor: "dji", MinRecords: 6})
}

func TestEmitsFlattenedNativeFieldsNotSimulated(t *testing.T) {
	s := startMock(t, mockfeed.New(mockfeed.Config{DroneIDs: []string{"D1"}, IncludeImagery: true}))
	a := newAdapter(t, s.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	out := make(chan vendor.RawRecord)
	go a.Subscribe(ctx, out) //nolint:errcheck // ends with ctx
	r := <-out
	if r.Vendor != "dji" || r.SourceDroneID != "D1" || r.Simulated {
		t.Fatalf("bad envelope: %+v", r)
	}
	for _, k := range []string{"battery_capacity_percent", "battery_voltage", "latitude", "timestamp"} {
		if _, ok := r.Payload[k]; !ok {
			t.Errorf("payload missing %q: %v", k, r.Payload)
		}
	}
	// Imagery is NOT the adapter's job to drop; the privacy filter does that.
	if _, ok := r.Payload["camera_frame_b64"]; !ok {
		t.Error("adapter must pass imagery through untouched for the privacy filter to drop")
	}
}

func TestDiscoversDevicesWhenNoneConfigured(t *testing.T) {
	s := startMock(t, mockfeed.New(mockfeed.Config{DroneIDs: []string{"A", "B", "C"}}))
	a := newAdapter(t, s.URL, nil)
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(a.devices) != 3 {
		t.Fatalf("devices = %v", a.devices)
	}
}

func TestConfiguredDevicesSkipDiscovery(t *testing.T) {
	s := startMock(t, mockfeed.New(mockfeed.Config{DroneIDs: []string{"A", "B"}}))
	a := newAdapter(t, s.URL, func(c *Config) { c.DeviceSerials = []string{"B"} })
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(a.devices) != 1 || a.devices[0] != "B" {
		t.Fatalf("devices = %v", a.devices)
	}
}

func TestAuthFailureIsPermanent(t *testing.T) {
	s := startMock(t, mockfeed.New(mockfeed.Config{}))
	a := newAdapter(t, s.URL, func(c *Config) { c.Credentials = creds("wrong") })
	err := a.Connect(context.Background())
	if !errors.Is(err, ErrAuth) || !errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("err = %v", err)
	}
	if a.Health().State != vendor.HealthDown {
		t.Errorf("health = %v", a.Health().State)
	}
	if strings.Contains(a.Health().LastError, "wrong") {
		t.Error("token leaked into health error")
	}
}

func TestMissingCredentialsArePermanent(t *testing.T) {
	a := newAdapter(t, "http://127.0.0.1:1", func(c *Config) { c.Credentials = credentials.StaticProvider{} })
	if err := a.Connect(context.Background()); !errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("err = %v", err)
	}
	b := newAdapter(t, "http://127.0.0.1:1", func(c *Config) { c.Credentials = credentials.StaticProvider{"dji": {"other": "x"}} })
	if err := b.Connect(context.Background()); !errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("missing token key: %v", err)
	}
}

func TestVendorDownAtConnectIsRetryable(t *testing.T) {
	s := startMock(t, mockfeed.New(mockfeed.Config{Faults: mockfeed.Faults{Down: true}}))
	a := newAdapter(t, s.URL, nil)
	err := a.Connect(context.Background())
	if err == nil || errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("a 503 must be a retryable error, got %v", err)
	}
	if a.Health().State != vendor.HealthDown {
		t.Errorf("health = %v", a.Health().State)
	}
}

func TestUnreachableVendorIsRetryable(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	url := s.URL
	s.Close() // nothing listens any more
	a := newAdapter(t, url, nil)
	if err := a.Connect(context.Background()); err == nil || errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("connection refused must be retryable, got %v", err)
	}
}

func TestOutageMidStreamReturnsErrorThenRecovers(t *testing.T) {
	feed := mockfeed.New(mockfeed.Config{DroneIDs: []string{"D1"}})
	s := startMock(t, feed)
	a := newAdapter(t, s.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatal(err)
	}

	out := make(chan vendor.RawRecord, 1000)
	done := make(chan error, 1)
	go func() { done <- a.Subscribe(ctx, out) }()
	<-out // healthy

	feed.SetFaults(mockfeed.Faults{Down: true})
	select {
	case err := <-done:
		if err == nil || errors.Is(err, vendor.ErrPermanent) {
			t.Fatalf("outage must yield a retryable error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe never gave up during the outage")
	}
	if a.Health().State != vendor.HealthDown {
		t.Errorf("health = %v, want down", a.Health().State)
	}

	// The connector manager would now back off and reconnect:
	feed.SetFaults(mockfeed.Faults{})
	if err := a.Connect(ctx); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	go func() { done <- a.Subscribe(ctx, out) }()
	for len(out) > 0 {
		<-out
	}
	select {
	case <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("no data after recovery")
	}
	if a.Health().State != vendor.HealthHealthy {
		t.Errorf("health after recovery = %v", a.Health().State)
	}
}

func TestSingleBadCycleToleratedThenHeals(t *testing.T) {
	var calls atomic.Int32
	feed := mockfeed.New(mockfeed.Config{DroneIDs: []string{"D1"}})
	inner := mockfeed.NewServer(feed, mockfeed.ServerConfig{Token: token})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/telemetry") && calls.Add(1) == 2 {
			http.Error(w, "boom", http.StatusBadGateway) // exactly one failure
			return
		}
		inner.ServeHTTP(w, r)
	}))
	defer s.Close()

	a := newAdapter(t, s.URL, func(c *Config) { c.MaxConsecutiveFailures = 3 })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	out := make(chan vendor.RawRecord, 100)
	done := make(chan error, 1)
	go func() { done <- a.Subscribe(ctx, out) }()
	for i := 0; i < 4; i++ { // records before and after the failed poll
		select {
		case <-out:
		case err := <-done:
			t.Fatalf("Subscribe ended on a single failure: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out")
		}
	}
	if st := a.Health().State; st != vendor.HealthHealthy {
		t.Errorf("health = %v, want healthy after recovery", st)
	}
}

func TestMalformedResponsesAreFailuresNotCrashes(t *testing.T) {
	feed := mockfeed.New(mockfeed.Config{DroneIDs: []string{"D1"},
		Faults: mockfeed.Faults{MalformedRate: 1, MalformedKinds: []mockfeed.MalformedKind{mockfeed.KindBrokenJSON}}})
	s := startMock(t, feed)
	a := newAdapter(t, s.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	err := a.Subscribe(ctx, make(chan vendor.RawRecord, 10))
	if err == nil || !strings.Contains(err.Error(), "consecutive failed poll cycles") {
		t.Fatalf("err = %v", err)
	}
}

func TestFieldLevelMalformedDataIsPassedThroughForValidation(t *testing.T) {
	feed := mockfeed.New(mockfeed.Config{DroneIDs: []string{"D1"},
		Faults: mockfeed.Faults{MalformedRate: 1, MalformedKinds: []mockfeed.MalformedKind{mockfeed.KindOutOfRange}}})
	s := startMock(t, feed)
	a := newAdapter(t, s.URL, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	out := make(chan vendor.RawRecord)
	go a.Subscribe(ctx, out) //nolint:errcheck
	if r := <-out; r.Payload["latitude"] != 123.4 {
		t.Fatalf("adapter must not 'fix' bad values; got %v", r.Payload["latitude"])
	}
}

func TestSlowVendorHitsRequestTimeout(t *testing.T) {
	feed := mockfeed.New(mockfeed.Config{DroneIDs: []string{"D1"}})
	s := startMock(t, feed)
	a := newAdapter(t, s.URL, func(c *Config) { c.RequestTimeout = 40 * time.Millisecond })
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	feed.SetFaults(mockfeed.Faults{Latency: 400 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := a.Subscribe(ctx, make(chan vendor.RawRecord, 10))
	if err == nil || errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("slow vendor should surface a retryable error, got %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no url":      {Credentials: creds("x")},
		"bad url":     {BaseURL: "ftp://x", Credentials: creds("x")},
		"no host":     {BaseURL: "http://", Credentials: creds("x")},
		"no provider": {BaseURL: "http://x"},
	} {
		if _, err := New(cfg); !errors.Is(err, vendor.ErrPermanent) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSubscribeBeforeConnect(t *testing.T) {
	a := newAdapter(t, "http://127.0.0.1:1", nil)
	if err := a.Subscribe(context.Background(), make(chan vendor.RawRecord)); !errors.Is(err, vendor.ErrPermanent) {
		t.Fatalf("err = %v", err)
	}
}

func TestCustomAuthHeaderAndPaths(t *testing.T) {
	var gotHeader, gotPath string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":[{"device_sn":"Z9"}]}`))
	}))
	defer s.Close()
	a := newAdapter(t, s.URL+"/", func(c *Config) { // trailing slash is trimmed
		c.AuthHeader = "X-Api-Key"
		c.DevicesPath = "/v2/aircraft"
	})
	if err := a.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotHeader != token || gotPath != "/v2/aircraft" {
		t.Fatalf("header=%q path=%q", gotHeader, gotPath)
	}
}

func TestAPIErrorCodeIsRetryableFailure(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":10001,"message":"rate limited","data":null}`))
	}))
	defer s.Close()
	a := newAdapter(t, s.URL, nil)
	err := a.Connect(context.Background())
	if err == nil || errors.Is(err, vendor.ErrPermanent) || !strings.Contains(err.Error(), "10001") {
		t.Fatalf("err = %v", err)
	}
}
