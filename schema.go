package telemetry

import (
	"fmt"
	"math"
	"time"
)

const SchemaVersion = "1.0"

// Source identifies the origin/protocol of a telemetry record.
type Source string

const (
	SourceMAVLink Source = "mavlink_ardupilot"
	SourceDJI     Source = "dji"
	SourceCSV     Source = "csv_replay"
)

// Telemetry is AeroSync's versioned common telemetry representation.
// All distances are metres, speed is metres/second, angles are degrees,
// percentages are in [0,100], and ObservedAt is stored as UTC.
type Telemetry struct {
	SchemaVersion     string    `json:"schema_version"`
	EventID           string    `json:"event_id"`
	DroneID           string    `json:"drone_id"`
	Source            Source    `json:"source"`
	ObservedAt        time.Time `json:"observed_at"`
	LatitudeDeg       float64   `json:"latitude_deg"`
	LongitudeDeg      float64   `json:"longitude_deg"`
	AltitudeM         float64   `json:"altitude_m"`
	GroundSpeedMPS    *float64  `json:"ground_speed_mps,omitempty"`
	HeadingDeg        *float64  `json:"heading_deg,omitempty"`
	BatteryPercent    *float64  `json:"battery_percent,omitempty"`
	FlightTimeSeconds *float64  `json:"flight_time_seconds,omitempty"`
	BatteryCycles     *int      `json:"battery_cycles,omitempty"`
	FaultFlags        []string  `json:"fault_flags,omitempty"`
}

// Validate checks the common contract. More detailed operational validation
// (for example vendor-specific limits and duplicate detection) belongs downstream.
func (t Telemetry) Validate() error {
	if t.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q", SchemaVersion)
	}
	if t.EventID == "" {
		return fmt.Errorf("event_id is required")
	}
	if t.DroneID == "" {
		return fmt.Errorf("drone_id is required")
	}
	switch t.Source {
	case SourceMAVLink, SourceDJI, SourceCSV:
	default:
		return fmt.Errorf("unsupported source %q", t.Source)
	}
	if t.ObservedAt.IsZero() {
		return fmt.Errorf("observed_at is required")
	}
	_, offset := t.ObservedAt.Zone()
	if offset != 0 {
		return fmt.Errorf("observed_at must be UTC")
	}
	if math.IsNaN(t.LatitudeDeg) || math.IsInf(t.LatitudeDeg, 0) || t.LatitudeDeg < -90 || t.LatitudeDeg > 90 {
		return fmt.Errorf("latitude_deg must be between -90 and 90")
	}
	if math.IsNaN(t.LongitudeDeg) || math.IsInf(t.LongitudeDeg, 0) || t.LongitudeDeg < -180 || t.LongitudeDeg > 180 {
		return fmt.Errorf("longitude_deg must be between -180 and 180")
	}
	if math.IsNaN(t.AltitudeM) || math.IsInf(t.AltitudeM, 0) {
		return fmt.Errorf("altitude_m must be finite")
	}
	if err := finiteOptional("ground_speed_mps", t.GroundSpeedMPS); err != nil {
		return err
	}
	if t.GroundSpeedMPS != nil && *t.GroundSpeedMPS < 0 {
		return fmt.Errorf("ground_speed_mps cannot be negative")
	}
	if err := finiteOptional("heading_deg", t.HeadingDeg); err != nil {
		return err
	}
	if t.HeadingDeg != nil && (*t.HeadingDeg < 0 || *t.HeadingDeg >= 360) {
		return fmt.Errorf("heading_deg must be in [0,360)")
	}
	if err := finiteOptional("battery_percent", t.BatteryPercent); err != nil {
		return err
	}
	if t.BatteryPercent != nil && (*t.BatteryPercent < 0 || *t.BatteryPercent > 100) {
		return fmt.Errorf("battery_percent must be in [0,100]")
	}
	if err := finiteOptional("flight_time_seconds", t.FlightTimeSeconds); err != nil {
		return err
	}
	if t.FlightTimeSeconds != nil && *t.FlightTimeSeconds < 0 {
		return fmt.Errorf("flight_time_seconds cannot be negative")
	}
	if t.BatteryCycles != nil && *t.BatteryCycles < 0 {
		return fmt.Errorf("battery_cycles cannot be negative")
	}
	return nil
}

func finiteOptional(name string, value *float64) error {
	if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
		return fmt.Errorf("%s must be finite", name)
	}
	return nil
}
