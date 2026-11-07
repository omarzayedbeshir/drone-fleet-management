// Package normalize maps supported vendor fields into the published backend schema.
package normalize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/pipeline"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

type Normalizer struct{ Now func() time.Time }

var _ pipeline.Stage = (*Normalizer)(nil)

func New() *Normalizer             { return &Normalizer{} }
func (n *Normalizer) Name() string { return "telemetry-normalizer" }
func (n *Normalizer) Process(ctx context.Context, r telemetry.RawRecord) (telemetry.RawRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := n.Normalize(r)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var out telemetry.RawRecord
	err = json.Unmarshal(b, &out)
	return out, err
}
func (n *Normalizer) Normalize(r telemetry.RawRecord) (telemetry.Sample, error) {
	s := telemetry.Sample{FaultFlags: []string{}, Status: "unknown"}
	source := strings.ToLower(text(first(r, "source", "vendor", "protocol")))
	switch source {
	case "dji":
		s.Source = "dji"
	case "mavlink", "mavlink_ardupilot", "ardupilot":
		s.Source = "ardupilot"
	case "csv", "csv_replay", "replay":
		s.Source = "csv"
	default:
		return s, fmt.Errorf("unsupported telemetry source")
	}
	var err error
	s.ObservedAt, err = parseTime(first(r, "observed_at", "timestamp", "time_utc", "timestamp_ms"))
	if err != nil {
		return s, err
	}
	// Epoch microseconds are accepted only for explicitly named UTC fields;
	// MAVLink boot-relative timestamps require the live adapter's clock mapping.
	if _, ok := r["time_usec"]; ok && first(r, "observed_at", "timestamp", "time_utc", "timestamp_ms") == nil {
		return s, fmt.Errorf("time_usec requires a UTC timestamp from the adapter")
	}
	s.EventID = text(first(r, "event_id", "eventId"))
	if s.EventID == "" {
		identity := text(first(r, "drone_id", "drone_sn", "device_sn", "serial_number", "sysid"))
		if identity == "" {
			return s, fmt.Errorf("missing drone identifier")
		}
		h := sha256.Sum256([]byte(identity + "/" + s.ObservedAt.Format(time.RFC3339Nano)))
		s.EventID = hex.EncodeToString(h[:])
	}
	if v := first(r, "status"); v != nil {
		s.Status = text(v)
	}
	s.FlightTimeSeconds, err = counter(first(r, "flight_time_seconds", "flight_time_s", "total_flight_time"))
	if err != nil {
		return s, fmt.Errorf("invalid or missing flight counter")
	}
	s.BatteryCycles, err = counter(first(r, "battery_cycles", "cycle_count", "battery_loop_times"))
	if err != nil {
		return s, fmt.Errorf("invalid or missing battery cycle counter")
	}
	for _, field := range []struct {
		target **float64
		keys   []string
	}{
		{&s.Latitude, []string{"latitude", "latitude_deg", "lat"}}, {&s.Longitude, []string{"longitude", "longitude_deg", "lon", "lng"}},
		{&s.AltitudeM, []string{"altitude_m", "height", "altitude"}}, {&s.SpeedMPS, []string{"speed_mps", "horizontal_speed", "ground_speed_mps", "groundspeed"}},
		{&s.BatteryPercent, []string{"battery_percent", "battery_capacity_percent", "battery_remaining", "battery"}},
	} {
		v := first(r, field.keys...)
		if v != nil {
			f, ok := number(v)
			if !ok {
				return s, fmt.Errorf("invalid numeric telemetry field")
			}
			*field.target = &f
		}
	}
	// Native MAVLink lat/lon are scaled integers regardless of their magnitude.
	if s.Source == "ardupilot" {
		if r["lat"] != nil && r["latitude"] == nil && r["latitude_deg"] == nil && s.Latitude != nil {
			*s.Latitude /= 1e7
		}
		if r["lon"] != nil && r["longitude"] == nil && r["longitude_deg"] == nil && s.Longitude != nil {
			*s.Longitude /= 1e7
		}
	}
	for _, field := range []struct {
		target **float64
		key    string
		scale  float64
	}{
		{&s.AltitudeM, "altitude_mm", 0.001}, {&s.AltitudeM, "altitude_ft", 0.3048}, {&s.SpeedMPS, "speed_kmh", 1 / 3.6}, {&s.SpeedMPS, "speed_mph", 0.44704}, {&s.BatteryPercent, "battery_fraction", 100},
	} {
		if *field.target == nil && r[field.key] != nil {
			f, ok := number(r[field.key])
			if !ok {
				return s, fmt.Errorf("invalid numeric telemetry field")
			}
			f *= field.scale
			*field.target = &f
		}
	}
	if s.Source == "ardupilot" && s.AltitudeM == nil {
		if v := first(r, "alt", "relative_alt"); v != nil {
			f, ok := number(v)
			if !ok {
				return s, fmt.Errorf("invalid MAVLink altitude")
			}
			f /= 1000
			s.AltitudeM = &f
		}
	}
	switch flags := first(r, "fault_flags", "fault_codes", "faults", "errors").(type) {
	case []string:
		s.FaultFlags = flags
	case []any:
		for _, f := range flags {
			v, ok := f.(string)
			if !ok {
				return s, fmt.Errorf("invalid fault code")
			}
			s.FaultFlags = append(s.FaultFlags, v)
		}
	case string:
		if flags != "" {
			s.FaultFlags = strings.FieldsFunc(flags, func(c rune) bool { return c == ';' || c == ',' })
		}
	case nil:
	default:
		return s, fmt.Errorf("invalid fault codes")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	now := time.Now()
	if n.Now != nil {
		now = n.Now()
	}
	return telemetry.Decode(b, now)
}
func first(r telemetry.RawRecord, keys ...string) any {
	for _, k := range keys {
		if v, ok := r[k]; ok {
			return v
		}
	}
	return nil
}
func text(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
func number(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case int:
		f = float64(x)
	case int32:
		f = float64(x)
	case int64:
		f = float64(x)
	case string:
		var err error
		f, err = strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}
func counter(v any) (int64, error) {
	f, ok := number(v)
	if !ok || f < 0 || f >= float64(math.MaxInt64) || math.Trunc(f) != f {
		return 0, fmt.Errorf("invalid counter")
	}
	return int64(f), nil
}
func parseTime(v any) (time.Time, error) {
	if t, ok := v.(time.Time); ok {
		return t.UTC(), nil
	}
	if s, ok := v.(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC(), nil
		}
	}
	if n, ok := number(v); ok && n >= 0 && n < 1e14 {
		if n > 1e11 {
			return time.UnixMilli(int64(n)).UTC(), nil
		}
		return time.Unix(int64(n), 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("missing or invalid UTC timestamp")
}
