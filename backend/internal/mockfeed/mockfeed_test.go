package mockfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/adaptertest"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

var fixedNow = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }

func collect(t *testing.T, f *Feed, n int) []vendor.RawRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.cfg.MaxRecords = n
	out := make(chan vendor.RawRecord, n)
	if err := f.Subscribe(ctx, out); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	close(out)
	var recs []vendor.RawRecord
	for r := range out {
		recs = append(recs, r)
	}
	return recs
}

func TestFeedConformance(t *testing.T) {
	adaptertest.Run(t, func(t *testing.T) vendor.VendorAdapter {
		return New(Config{Interval: time.Millisecond})
	}, adaptertest.Options{WantSimulated: true})
}

func TestFeedIsDeterministicForSeed(t *testing.T) {
	a := collect(t, New(Config{Seed: 7, Now: fixedNow}), 30)
	b := collect(t, New(Config{Seed: 7, Now: fixedNow}), 30)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if !bytes.Equal(ja, jb) {
		t.Fatal("same seed must give identical output")
	}
}

func TestFeedRecordShapeIsFlatDJIStyle(t *testing.T) {
	recs := collect(t, New(Config{Now: fixedNow}), 3)
	p := recs[0].Payload
	for _, k := range []string{"device_sn", "timestamp", "latitude", "longitude",
		"battery_capacity_percent", "battery_voltage", "battery_loop_times", "total_flight_time", "fault_codes"} {
		if _, ok := p[k]; k != "fault_codes" && !ok {
			t.Errorf("missing key %q in %v", k, p)
		}
	}
	if recs[0].SourceDroneID != "MOCK-0001" || recs[1].SourceDroneID != "MOCK-0002" {
		t.Errorf("drones not emitted round-robin: %v %v", recs[0].SourceDroneID, recs[1].SourceDroneID)
	}
	if !recs[0].Simulated || recs[0].Vendor != "mock" {
		t.Errorf("record must be simulated/mock: %+v", recs[0])
	}
}

func TestImageryOnlyWhenRequested(t *testing.T) {
	plain := collect(t, New(Config{}), 1)[0].Payload
	if _, ok := plain["camera_frame_b64"]; ok {
		t.Fatal("imagery present without IncludeImagery")
	}
	img := collect(t, New(Config{IncludeImagery: true}), 1)[0].Payload
	if _, ok := img["camera_frame_b64"]; !ok {
		t.Fatal("imagery field missing with IncludeImagery")
	}
	if _, ok := img["payload_camera_stream_url"]; !ok {
		t.Fatalf("payload field missing (flattened): %v", img)
	}
}

func TestDownFault(t *testing.T) {
	f := New(Config{Faults: Faults{Down: true}})
	if err := f.Connect(context.Background()); !errors.Is(err, ErrVendorDown) {
		t.Fatalf("Connect err = %v", err)
	}
	if f.Health().State != vendor.HealthDown {
		t.Fatalf("health = %v", f.Health().State)
	}
	err := f.Subscribe(context.Background(), make(chan vendor.RawRecord, 1))
	if !errors.Is(err, ErrVendorDown) {
		t.Fatalf("Subscribe err = %v", err)
	}
}

func TestGoesDownMidStreamAndRecovers(t *testing.T) {
	f := New(Config{Interval: time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan vendor.RawRecord)
	done := make(chan error, 1)
	go func() { done <- f.Subscribe(ctx, out) }()
	<-out
	f.SetFaults(Faults{Down: true})
	go func() {
		for range out {
		}
	}() // drain whatever is in flight
	if err := <-done; !errors.Is(err, ErrVendorDown) {
		t.Fatalf("want ErrVendorDown, got %v", err)
	}
	f.SetFaults(Faults{})
	if err := f.Connect(ctx); err != nil {
		t.Fatalf("reconnect after recovery: %v", err)
	}
}

func TestSlowFault(t *testing.T) {
	f := New(Config{Faults: Faults{Latency: 60 * time.Millisecond}})
	start := time.Now()
	if err := f.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 55*time.Millisecond {
		t.Fatalf("Connect returned after %v, want >= 60ms", d)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := f.Connect(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow Connect must honour ctx, got %v", err)
	}
	f.setHealth(vendor.HealthHealthy, nil)
	f.SetFaults(Faults{Latency: time.Millisecond})
	collect(t, f, 1)
	if f.Health().State != vendor.HealthDegraded {
		t.Fatalf("slow vendor should be degraded, got %v", f.Health().State)
	}
}

func isValid(p map[string]any) bool {
	lat, ok1 := p["latitude"].(float64)
	lon, ok2 := p["longitude"].(float64)
	_, ok3 := p["timestamp"].(float64)
	pct, ok4 := p["battery_capacity_percent"].(float64)
	return ok1 && ok2 && ok3 && ok4 && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && pct >= 0 && pct <= 100
}

func TestMalformedKinds(t *testing.T) {
	cases := map[MalformedKind]func(map[string]any) bool{
		KindMissingField: func(p map[string]any) bool { return !isValid(p) && len(p) > 0 },
		KindWrongType:    func(p map[string]any) bool { _, s := p["latitude"].(string); return s },
		KindOutOfRange:   func(p map[string]any) bool { return p["latitude"] == 123.4 && p["battery_capacity_percent"] == 250.0 },
		KindBadTimestamp: func(p map[string]any) bool { return p["timestamp"] == "not-a-time" },
		KindEmpty:        func(p map[string]any) bool { return len(p) == 0 },
	}
	for kind, check := range cases {
		t.Run(string(kind), func(t *testing.T) {
			f := New(Config{Faults: Faults{MalformedRate: 1, MalformedKinds: []MalformedKind{kind}}})
			for i, r := range collect(t, f, 6) {
				if !check(r.Payload) {
					t.Fatalf("record %d not malformed as %s: %v", i, kind, r.Payload)
				}
			}
		})
	}
}

func TestDuplicateRepeatsPreviousRecord(t *testing.T) {
	f := New(Config{DroneIDs: []string{"A"}, Now: fixedNow})
	first := collect(t, f, 1)[0]
	f.SetFaults(Faults{MalformedRate: 1, MalformedKinds: []MalformedKind{KindDuplicate}})
	second := collect(t, f, 1)[0]
	a, _ := json.Marshal(first.Payload)
	b, _ := json.Marshal(second.Payload)
	if !bytes.Equal(a, b) {
		t.Fatalf("duplicate differs:\n%s\n%s", a, b)
	}
}

func TestZeroRateProducesOnlyValidRecords(t *testing.T) {
	for i, r := range collect(t, New(Config{}), 60) {
		if !isValid(r.Payload) {
			t.Fatalf("record %d invalid with no faults: %v", i, r.Payload)
		}
	}
}

func TestBrokenJSONNeverInAdapterMode(t *testing.T) {
	f := New(Config{Faults: Faults{MalformedRate: 1, MalformedKinds: []MalformedKind{KindBrokenJSON}}})
	// intersect(adapterKinds, [broken_json]) is empty: records pass through intact.
	for _, r := range collect(t, f, 3) {
		if !isValid(r.Payload) {
			t.Fatalf("expected intact record, got %v", r.Payload)
		}
	}
}

// ---- HTTP server ----

func newTestServer(t *testing.T, feed *Feed, token string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(NewServer(feed, ServerConfig{Token: token}))
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, url, token string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set(DefaultAuthHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.String()
}

func TestServerAuthDevicesAndTelemetry(t *testing.T) {
	feed := New(Config{DroneIDs: []string{"D1", "D2"}})
	s := newTestServer(t, feed, "tok")

	if resp, _ := get(t, s.URL+"/devices", ""); resp.StatusCode != 401 {
		t.Fatalf("no token: status %d", resp.StatusCode)
	}
	if resp, _ := get(t, s.URL+"/devices", "wrong"); resp.StatusCode != 401 {
		t.Fatalf("wrong token: status %d", resp.StatusCode)
	}
	resp, body := get(t, s.URL+"/devices", "tok")
	if resp.StatusCode != 200 || !strings.Contains(body, `"D1"`) || !strings.Contains(body, `"D2"`) {
		t.Fatalf("devices: %d %s", resp.StatusCode, body)
	}

	resp, body = get(t, s.URL+"/devices/D1/telemetry", "tok")
	var env struct {
		Code int
		Data map[string]any
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || resp.StatusCode != 200 {
		t.Fatalf("telemetry: %d %v %s", resp.StatusCode, err, body)
	}
	if env.Data["device_sn"] != "D1" || env.Data["battery"] == nil {
		t.Fatalf("telemetry should be nested native form: %v", env.Data)
	}
	if resp, _ := get(t, s.URL+"/devices/NOPE/telemetry", "tok"); resp.StatusCode != 404 {
		t.Fatalf("unknown device: %d", resp.StatusCode)
	}
}

func TestServerFaultsOverHTTP(t *testing.T) {
	feed := New(Config{DroneIDs: []string{"D1"}})
	s := newTestServer(t, feed, "")

	put := func(body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPut, s.URL+"/admin/faults", strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	if r := put(`{"down":true}`); r.StatusCode != 200 {
		t.Fatalf("PUT down: %d", r.StatusCode)
	}
	if resp, _ := get(t, s.URL+"/devices/D1/telemetry", ""); resp.StatusCode != 503 {
		t.Fatalf("down: status %d", resp.StatusCode)
	}
	if resp, _ := get(t, s.URL+"/healthz", ""); resp.StatusCode != 200 {
		t.Fatal("healthz must not be faulted")
	}

	put(`{"malformed_rate":1,"malformed_kinds":["broken_json"]}`)
	_, body := get(t, s.URL+"/devices/D1/telemetry", "")
	if json.Valid([]byte(body)) {
		t.Fatalf("expected truncated JSON, got valid: %s", body)
	}

	put(`{}`)
	if resp, body := get(t, s.URL+"/devices/D1/telemetry", ""); resp.StatusCode != 200 || !json.Valid([]byte(body)) {
		t.Fatalf("after clearing faults: %d %s", resp.StatusCode, body)
	}

	for _, bad := range []string{`{"malformed_rate":2}`, `{"latency_ms":-1}`, `{"malformed_kinds":["nope"]}`, `{"bogus":1}`, `not json`} {
		if r := put(bad); r.StatusCode != 400 {
			t.Errorf("PUT %s: status %d, want 400", bad, r.StatusCode)
		}
	}
}

func TestServerLatencyHonoursClientTimeout(t *testing.T) {
	feed := New(Config{DroneIDs: []string{"D1"}, Faults: Faults{Latency: 500 * time.Millisecond}})
	s := newTestServer(t, feed, "")
	c := &http.Client{Timeout: 50 * time.Millisecond}
	if _, err := c.Get(s.URL + "/devices"); err == nil {
		t.Fatal("expected client timeout against slow vendor")
	}
}
