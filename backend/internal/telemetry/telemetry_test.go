package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func document(changes map[string]any) []byte {
	value := map[string]any{"event_id": "event-1", "source": "dji", "observed_at": "2026-10-29T11:00:00Z", "status": "available", "flight_time_seconds": 100, "battery_cycles": 3, "fault_flags": []string{}}
	for key, v := range changes {
		value[key] = v
	}
	data, _ := json.Marshal(value)
	return data
}
func TestNormalizedSchema(t *testing.T) {
	first, err := Decode(document(map[string]any{"observed_at": "2026-10-29T13:00:00.123456789+02:00", "fault_flags": []string{"GPS", "BATTERY", "GPS"}}), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if first.ObservedAt.Location() != time.UTC || first.ObservedAt.Nanosecond() != 123456000 || strings.Join(first.FaultFlags, ",") != "BATTERY,GPS" {
		t.Fatalf("not canonical: %+v", first)
	}
	second, err := Decode(document(map[string]any{"observed_at": "2026-10-29T11:00:00.123456Z", "fault_flags": []string{"BATTERY", "GPS"}}), testNow)
	if err != nil {
		t.Fatal(err)
	}
	firstHash, err := fingerprint(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := fingerprint(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstHash) != string(secondHash) {
		t.Fatal("equivalent normalized samples have different hashes")
	}
}
func TestInvalidTelemetry(t *testing.T) {
	for name, changes := range map[string]map[string]any{
		"missing flags": {"fault_flags": nil}, "missing counter": {"flight_time_seconds": nil}, "negative counter": {"battery_cycles": -1}, "fractional counter": {"flight_time_seconds": 1.5},
		"missing event": {"event_id": ""}, "invalid event": {"event_id": "bad id"}, "source": {"source": "other"}, "status": {"status": "flying"},
		"timestamp": {"observed_at": "not a timestamp"}, "future": {"observed_at": "2026-10-29T12:06:00Z"}, "epoch": {"observed_at": "1969-12-31T23:59:59Z"},
		"battery": {"battery_percent": 101}, "latitude": {"latitude": 91, "longitude": 31}, "longitude": {"latitude": 30, "longitude": 181},
		"coordinate pair": {"latitude": 30}, "speed": {"speed_mps": -1}, "altitude": {"altitude_m": 100001}, "fault": {"fault_flags": []string{"has spaces"}},
		"unknown field": {"imagery": "SECRET-IMAGE"}, "too many faults": {"fault_flags": make([]string, 33)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(document(changes), testNow); err == nil {
				t.Fatal("accepted invalid sample")
			}
		})
	}
	for _, raw := range []string{"{", "null", "[]", string(document(nil)) + " {}", `{"battery_percent":1e999}`} {
		if _, err := Decode([]byte(raw), testNow); err == nil {
			t.Fatalf("accepted invalid JSON/schema: %s", raw)
		}
	}
	if _, err := Decode([]byte(strings.Repeat("x", MaxBody+1)), testNow); err == nil {
		t.Fatal("accepted oversized body")
	}
}
