// Package privacy applies an allowlist immediately after raw adapter output.
package privacy

import (
	"context"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

type Filter struct{}

func NewFilter() *Filter       { return &Filter{} }
func (f *Filter) Name() string { return "privacy-filter" }

// Only scalar telemetry and scalar fault codes may cross the raw boundary.
// Arbitrary maps, media, arrays of objects and unknown fields never enter buffers.
func (f *Filter) Process(ctx context.Context, r telemetry.RawRecord) (telemetry.RawRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	out := telemetry.RawRecord{}
	for _, k := range []string{"source", "vendor", "protocol", "drone_id", "drone_sn", "device_sn", "serial_number", "sysid", "event_id", "eventId", "observed_at", "timestamp", "time_utc", "timestamp_ms", "status", "flight_time_seconds", "flight_time_s", "total_flight_time", "battery_cycles", "cycle_count", "battery_loop_times", "latitude", "latitude_deg", "lat", "longitude", "longitude_deg", "lon", "lng", "altitude_m", "height", "altitude", "altitude_mm", "altitude_ft", "alt", "relative_alt", "speed_mps", "horizontal_speed", "ground_speed_mps", "groundspeed", "speed_kmh", "speed_mph", "battery_percent", "battery_capacity_percent", "battery_remaining", "battery", "battery_fraction"} {
		if v, ok := r[k]; ok {
			switch v.(type) {
			case string, bool, float64, int, int32, int64:
				out[k] = v
			}
		}
	}
	for _, k := range []string{"fault_flags", "fault_codes", "faults", "errors"} {
		switch v := r[k].(type) {
		case string:
			out[k] = v
		case []string:
			out[k] = append([]string{}, v...)
		case []any:
			copy := make([]any, 0, len(v))
			valid := true
			for _, x := range v {
				if _, ok := x.(string); !ok {
					valid = false
					break
				}
				copy = append(copy, x)
			}
			if valid {
				out[k] = copy
			}
		}
	}
	return out, nil
}
