package normalize

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aerosync/telemetry-pipeline/pipeline"
	"aerosync/telemetry-pipeline/telemetry"
)

type Normalizer struct{}

var _ pipeline.Stage = (*Normalizer)(nil)

func New() *Normalizer             { return &Normalizer{} }
func (n *Normalizer) Name() string { return "telemetry-normalizer" }

func (n *Normalizer) Process(ctx context.Context, record telemetry.RawRecord) (telemetry.RawRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t, err := n.Normalize(record)
	if err != nil {
		return nil, err
	}
	return toRaw(t), nil
}

func (n *Normalizer) Normalize(r telemetry.RawRecord) (telemetry.Telemetry, error) {
	if r == nil {
		return telemetry.Telemetry{}, fmt.Errorf("record is nil")
	}
	source, err := detectSource(r)
	if err != nil {
		return telemetry.Telemetry{}, err
	}
	droneID := firstString(r, "drone_id", "droneId", "drone_sn", "serial_number", "sysid", "system_id", "vehicle_id")
	if droneID == "" {
		return telemetry.Telemetry{}, fmt.Errorf("missing drone identifier")
	}
	observedAt, err := parseTime(first(r, "observed_at", "timestamp", "time_utc", "time_usec", "timestamp_ms"))
	if err != nil {
		return telemetry.Telemetry{}, fmt.Errorf("timestamp: %w", err)
	}

	lat, ok := firstFloat(r, "latitude_deg", "latitude", "lat")
	if !ok {
		return telemetry.Telemetry{}, fmt.Errorf("missing latitude")
	}
	if source == telemetry.SourceMAVLink && abs(lat) > 90 {
		lat /= 1e7
	}
	lon, ok := firstFloat(r, "longitude_deg", "longitude", "lon", "lng")
	if !ok {
		return telemetry.Telemetry{}, fmt.Errorf("missing longitude")
	}
	if source == telemetry.SourceMAVLink && abs(lon) > 180 {
		lon /= 1e7
	}

	altitude, ok := firstFloat(r, "altitude_m", "altitude", "alt", "relative_alt")
	if !ok {
		if feet, feetOK := firstFloat(r, "altitude_ft"); feetOK {
			altitude, ok = feet*0.3048, true
		}
	}
	if !ok {
		return telemetry.Telemetry{}, fmt.Errorf("missing altitude")
	}
	if mm, exists := firstFloat(r, "altitude_mm"); exists {
		altitude = mm / 1000
	} else if source == telemetry.SourceMAVLink && (r["alt"] != nil || r["relative_alt"] != nil) {
		altitude /= 1000
	} else if feet, exists := firstFloat(r, "altitude_ft"); exists {
		altitude = feet * 0.3048
	}

	t := telemetry.Telemetry{
		SchemaVersion: telemetry.SchemaVersion,
		EventID:       firstString(r, "event_id", "eventId", "message_id"),
		DroneID:       droneID, Source: source, ObservedAt: observedAt.UTC(),
		LatitudeDeg: lat, LongitudeDeg: lon, AltitudeM: altitude,
	}
	if t.EventID == "" {
		t.EventID = fmt.Sprintf("%s-%d", droneID, observedAt.UnixNano())
	}
	if speed, ok := firstFloat(r, "ground_speed_mps", "groundspeed", "speed_mps"); ok {
		t.GroundSpeedMPS = ptr(speed)
	} else if speed, ok := firstFloat(r, "speed_kmh", "speed"); ok {
		t.GroundSpeedMPS = ptr(speed / 3.6)
	} else if speed, ok := firstFloat(r, "speed_mph"); ok {
		t.GroundSpeedMPS = ptr(speed * 0.44704)
	}
	if heading, ok := firstFloat(r, "heading_deg", "heading", "yaw"); ok {
		if source == telemetry.SourceMAVLink && heading > 360 {
			heading /= 100
		}
		t.HeadingDeg = ptr(normalizeHeading(heading))
	}
	if battery, ok := firstFloat(r, "battery_percent", "battery_remaining", "battery"); ok {
		if battery >= 0 && battery <= 1 {
			battery *= 100
		}
		t.BatteryPercent = ptr(battery)
	}
	if seconds, ok := firstFloat(r, "flight_time_seconds", "flight_time_s"); ok {
		t.FlightTimeSeconds = ptr(seconds)
	} else if minutes, ok := firstFloat(r, "flight_time_minutes"); ok {
		t.FlightTimeSeconds = ptr(minutes * 60)
	}
	if cycles, ok := firstInt(r, "battery_cycles", "cycle_count"); ok {
		t.BatteryCycles = &cycles
	}
	switch flags := first(r, "fault_flags", "faults", "errors").(type) {
	case []any:
		for _, flag := range flags {
			t.FaultFlags = append(t.FaultFlags, fmt.Sprint(flag))
		}
	case []string:
		t.FaultFlags = append(t.FaultFlags, flags...)
	case string:
		if flags != "" {
			t.FaultFlags = strings.Split(flags, ",")
		}
	}
	if err := t.Validate(); err != nil {
		return telemetry.Telemetry{}, err
	}
	return t, nil
}

func detectSource(r telemetry.RawRecord) (telemetry.Source, error) {
	s := strings.ToLower(firstString(r, "source", "vendor", "protocol"))
	switch {
	case strings.Contains(s, "mav"), strings.Contains(s, "ardupilot"):
		return telemetry.SourceMAVLink, nil
	case strings.Contains(s, "dji"):
		return telemetry.SourceDJI, nil
	case strings.Contains(s, "csv"), strings.Contains(s, "replay"):
		return telemetry.SourceCSV, nil
	case s != "":
		return "", fmt.Errorf("unknown telemetry source %q", s)
	case r["sysid"] != nil || r["time_usec"] != nil:
		return telemetry.SourceMAVLink, nil
	case r["drone_sn"] != nil:
		return telemetry.SourceDJI, nil
	default:
		return telemetry.SourceCSV, nil
	}
}
func parseTime(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x.UTC(), nil
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t.UTC(), nil
			}
		}
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return unixTime(n), nil
		}
	case int:
		return unixTime(int64(x)), nil
	case int64:
		return unixTime(x), nil
	case float64:
		return unixTime(int64(x)), nil
	}
	return time.Time{}, fmt.Errorf("unsupported or missing timestamp %v", v)
}
func unixTime(n int64) time.Time {
	if n > 1e14 {
		return time.Unix(0, n*1000).UTC()
	}
	if n > 1e11 {
		return time.UnixMilli(n).UTC()
	}
	return time.Unix(n, 0).UTC()
}
func first(r telemetry.RawRecord, keys ...string) any {
	for _, k := range keys {
		if v, ok := r[k]; ok {
			return v
		}
	}
	return nil
}
func firstString(r telemetry.RawRecord, keys ...string) string {
	v := first(r, keys...)
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case fmt.Stringer:
		return x.String()
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
func firstFloat(r telemetry.RawRecord, keys ...string) (float64, bool) {
	switch x := first(r, keys...).(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}
func firstInt(r telemetry.RawRecord, keys ...string) (int, bool) {
	if v, ok := firstFloat(r, keys...); ok {
		return int(v), true
	}
	return 0, false
}
func ptr(v float64) *float64 { return &v }
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func normalizeHeading(v float64) float64 {
	v = float64(int(v*1000)%360000) / 1000
	if v < 0 {
		v += 360
	}
	return v
}
func toRaw(t telemetry.Telemetry) telemetry.RawRecord {
	r := telemetry.RawRecord{
		"schema_version": t.SchemaVersion, "event_id": t.EventID, "drone_id": t.DroneID,
		"source": string(t.Source), "observed_at": t.ObservedAt.Format(time.RFC3339Nano),
		"latitude_deg": t.LatitudeDeg, "longitude_deg": t.LongitudeDeg, "altitude_m": t.AltitudeM,
	}
	if t.GroundSpeedMPS != nil {
		r["ground_speed_mps"] = *t.GroundSpeedMPS
	}
	if t.HeadingDeg != nil {
		r["heading_deg"] = *t.HeadingDeg
	}
	if t.BatteryPercent != nil {
		r["battery_percent"] = *t.BatteryPercent
	}
	if t.FlightTimeSeconds != nil {
		r["flight_time_seconds"] = *t.FlightTimeSeconds
	}
	if t.BatteryCycles != nil {
		r["battery_cycles"] = *t.BatteryCycles
	}
	if len(t.FaultFlags) > 0 {
		r["fault_flags"] = t.FaultFlags
	}
	return r
}
