package contract_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/buffer"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/normalize"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/privacy"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
	"reflect"
	"testing"
	"time"
)

func fixture() telemetry.RawRecord {
	return telemetry.RawRecord{"source": "dji", "device_sn": "DJI-001", "timestamp": "2026-11-07T08:00:00Z", "latitude": 30., "longitude": 31., "height": 50., "horizontal_speed": 5.5, "battery_capacity_percent": 1., "total_flight_time": 3600., "battery_loop_times": 20., "fault_codes": "GPS;MOTOR"}
}
func TestCommonContract(t *testing.T) {
	r := fixture()
	s, err := normalize.New().Normalize(r)
	if err != nil {
		t.Fatal(err)
	}
	if *s.BatteryPercent != 1 || *s.SpeedMPS != 5.5 || s.BatteryCycles != 20 || s.FlightTimeSeconds != 3600 || *s.AltitudeM != 50 {
		t.Fatalf("bad normalization: %+v", s)
	}
	b, _ := json.Marshal(s)
	if _, err = telemetry.Decode(b, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, pct := range []float64{0, 0.5, 1, 100} {
		r["battery_capacity_percent"] = pct
		s, err := normalize.New().Normalize(r)
		if err != nil || *s.BatteryPercent != pct {
			t.Fatalf("percent %v changed: %+v %v", pct, s, err)
		}
	}
	r = fixture()
	r["source"] = "ardupilot"
	delete(r, "latitude")
	delete(r, "longitude")
	r["lat"] = 1
	r["lon"] = 2
	s, err = normalize.New().Normalize(r)
	if err != nil || *s.Latitude != 1e-7 || *s.Longitude != 2e-7 {
		t.Fatalf("small scaled coordinates: %+v %v", s, err)
	}
}
func TestPrivacyBeforeBuffer(t *testing.T) {
	r := fixture()
	for _, k := range []string{"camera_frame_b64", "videoStream", "payload", "unknown"} {
		r[k] = "secret"
	}
	r["fault_codes"] = []any{map[string]any{"image": "secret"}}
	clean, err := privacy.NewFilter().Process(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(clean)
	for _, k := range []string{"camera_frame_b64", "videoStream", "payload", "unknown", "fault_codes"} {
		if _, ok := clean[k]; ok {
			t.Fatalf("private field %s survived: %s", k, b)
		}
	}
	q, _ := buffer.New(1)
	if err = q.Enqueue(context.Background(), clean); err != nil {
		t.Fatal(err)
	}
	q.Close()
	q.Close()
	got, err := q.Dequeue(context.Background())
	if err != nil || !reflect.DeepEqual(clean, got) {
		t.Fatal("buffer changed filtered record")
	}
	if _, err = q.Dequeue(context.Background()); !errors.Is(err, buffer.ErrClosed) {
		t.Fatal(err)
	}
}
func TestBufferBackpressureAndOwnership(t *testing.T) {
	q, _ := buffer.New(1)
	r := telemetry.RawRecord{"fault_flags": []string{"GPS"}}
	_ = q.Enqueue(context.Background(), r)
	r["fault_flags"].([]string)[0] = "changed"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := q.Enqueue(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	got, _ := q.Dequeue(context.Background())
	if got["fault_flags"].([]string)[0] != "GPS" {
		t.Fatal("buffer aliases input")
	}
}
