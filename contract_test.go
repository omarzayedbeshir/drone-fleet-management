package contract_test

import (
	"context"
	"testing"
	"time"

	"aerosync/telemetry-pipeline/buffer"
	"aerosync/telemetry-pipeline/normalize"
	"aerosync/telemetry-pipeline/privacy"
	"aerosync/telemetry-pipeline/telemetry"
)

func TestNormalizedRecordsSatisfyCommonContract(t *testing.T) {
	cases := []telemetry.RawRecord{
		{
			"source": "mavlink", "sysid": 7, "time_usec": int64(1794000000000000),
			"lat": 300000000, "lon": 311000000, "alt": 125000,
			"groundspeed": 12.5, "heading": 9000, "battery_remaining": 0.75,
		},
		{
			"source": "dji", "drone_sn": "DJI-001", "timestamp": "2026-11-07T10:00:00+02:00",
			"latitude": 30.0, "longitude": 31.1, "altitude_m": 125.0,
			"speed_kmh": 36.0, "battery": 80.0,
		},
		{
			"source": "csv", "drone_id": "CSV-001", "observed_at": "2026-11-07T08:00:00Z",
			"latitude_deg": 30.0, "longitude_deg": 31.1, "altitude_ft": 100.0,
			"speed_mph": 10.0, "flight_time_minutes": 2,
		},
	}
	n := normalize.New()
	for i, raw := range cases {
		got, err := n.Normalize(raw)
		if err != nil {
			t.Fatalf("case %d: unexpected normalization error: %v", i, err)
		}
		if err := got.Validate(); err != nil {
			t.Fatalf("case %d: contract failed: %v", i, err)
		}
		if got.ObservedAt.Location() != time.UTC {
			t.Fatalf("case %d: timestamp is not UTC", i)
		}
	}
}

func TestPrivacyFilterRunsBeforeBufferAndRemovesNestedPrivateData(t *testing.T) {
	raw := telemetry.RawRecord{
		"drone_id": "D-1", "camera_image": []byte("secret"), "videoStream": "secret",
		"payload":   map[string]any{"measurement": 42},
		"telemetry": map[string]any{"altitude_m": 10.0, "image_data": "secret"},
	}
	filtered, err := privacy.NewFilter().Process(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := filtered["camera_image"]; ok {
		t.Fatal("camera image was not removed")
	}
	if _, ok := filtered["videoStream"]; ok {
		t.Fatal("video was not removed")
	}
	if _, ok := filtered["payload"]; ok {
		t.Fatal("payload was not removed")
	}
	nested := filtered["telemetry"].(map[string]any)
	if _, ok := nested["image_data"]; ok {
		t.Fatal("nested image was not removed")
	}

	q, err := buffer.New(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(context.Background(), filtered); err != nil {
		t.Fatal(err)
	}
	dequeued, err := q.Dequeue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dequeued["camera_image"]; ok {
		t.Fatal("private field reached ingress buffer")
	}
}

func TestBufferBackpressureHonorsCancellation(t *testing.T) {
	q, err := buffer.New(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(context.Background(), telemetry.RawRecord{"n": 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := q.Enqueue(ctx, telemetry.RawRecord{"n": 2}); err == nil {
		t.Fatal("enqueue should block and return context deadline error when full")
	}
	if q.Len() != 1 {
		t.Fatalf("expected one buffered record, got %d", q.Len())
	}
}

func TestBufferRejectsInvalidCapacity(t *testing.T) {
	if _, err := buffer.New(0); err == nil {
		t.Fatal("expected invalid capacity error")
	}
}
