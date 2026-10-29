// Package telemetry validates and persists normalized vendor observations.
package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"sort"
	"time"
)

const MaxBody = 16384

var ErrNotFound = errors.New("drone not found")

type Rejection struct {
	Code    string
	Message string
}

func (e *Rejection) Error() string      { return e.Message }
func reject(code, message string) error { return &Rejection{Code: code, Message: message} }

type Sample struct {
	EventID           string    `json:"event_id"`
	Source            string    `json:"source"`
	ObservedAt        time.Time `json:"observed_at"`
	Status            string    `json:"status"`
	FlightTimeSeconds int64     `json:"flight_time_seconds"`
	BatteryCycles     int64     `json:"battery_cycles"`
	BatteryPercent    *float64  `json:"battery_percent"`
	Latitude          *float64  `json:"latitude"`
	Longitude         *float64  `json:"longitude"`
	AltitudeM         *float64  `json:"altitude_m"`
	SpeedMPS          *float64  `json:"speed_mps"`
	FaultFlags        []string  `json:"fault_flags"`
}
type Stored struct {
	Sample
	ReceivedAt time.Time `json:"received_at"`
}
type Result struct {
	Duplicate     bool   `json:"duplicate"`
	LatestUpdated bool   `json:"latest_updated"`
	Sample        Stored `json:"sample"`
}
type Store interface {
	Ingest(context.Context, string, string, string, []byte) (Result, error)
	List(context.Context, string, string, int, int) ([]Stored, bool, error)
}

var eventPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var flagPattern = regexp.MustCompile(`^[A-Za-z0-9_:-]{1,64}$`)

func Decode(raw []byte, now time.Time) (Sample, error) {
	var s Sample
	if len(raw) > MaxBody {
		return s, reject("body_too_large", "Telemetry body exceeds 16 KiB")
	}
	// Pointers distinguish absent counters from legitimate zero readings.
	var wire struct {
		EventID           string    `json:"event_id"`
		Source            string    `json:"source"`
		ObservedAt        time.Time `json:"observed_at"`
		Status            string    `json:"status"`
		FlightTimeSeconds *int64    `json:"flight_time_seconds"`
		BatteryCycles     *int64    `json:"battery_cycles"`
		BatteryPercent    *float64  `json:"battery_percent"`
		Latitude          *float64  `json:"latitude"`
		Longitude         *float64  `json:"longitude"`
		AltitudeM         *float64  `json:"altitude_m"`
		SpeedMPS          *float64  `json:"speed_mps"`
		FaultFlags        *[]string `json:"fault_flags"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return s, reject("invalid_sample", "Invalid telemetry document or unsupported field")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return s, reject("invalid_sample", "Provide exactly one telemetry object")
	}
	if wire.FlightTimeSeconds == nil || wire.BatteryCycles == nil || wire.FaultFlags == nil {
		return s, reject("invalid_sample", "flight_time_seconds, battery_cycles, and fault_flags are required")
	}
	s = Sample{EventID: wire.EventID, Source: wire.Source, ObservedAt: wire.ObservedAt.UTC().Truncate(time.Microsecond), Status: wire.Status,
		FlightTimeSeconds: *wire.FlightTimeSeconds, BatteryCycles: *wire.BatteryCycles, BatteryPercent: wire.BatteryPercent, Latitude: wire.Latitude, Longitude: wire.Longitude,
		AltitudeM: wire.AltitudeM, SpeedMPS: wire.SpeedMPS, FaultFlags: *wire.FaultFlags}
	if !eventPattern.MatchString(s.EventID) {
		return s, reject("invalid_sample", "event_id must be a stable 1–128 character source identifier")
	}
	if s.Source != "dji" && s.Source != "ardupilot" && s.Source != "csv" {
		return s, reject("invalid_sample", "source must be dji, ardupilot, or csv")
	}
	switch s.Status {
	case "available", "charging", "in_mission", "grounded", "unknown":
	default:
		return s, reject("invalid_sample", "Invalid operational status")
	}
	if s.ObservedAt.Before(time.Unix(0, 0)) || s.ObservedAt.After(now.Add(5*time.Minute)) {
		return s, reject("invalid_sample", "observed_at must be a timestamp since 1970 and at most five minutes in the future")
	}
	if s.FlightTimeSeconds < 0 || s.BatteryCycles < 0 {
		return s, reject("invalid_sample", "Cumulative counters cannot be negative")
	}
	for _, bound := range []struct {
		value    *float64
		min, max float64
	}{
		{s.BatteryPercent, 0, 100}, {s.Latitude, -90, 90}, {s.Longitude, -180, 180}, {s.AltitudeM, -12000, 100000}, {s.SpeedMPS, 0, 500},
	} {
		if bound.value != nil {
			v := *bound.value
			if math.IsNaN(v) || math.IsInf(v, 0) || v < bound.min || v > bound.max {
				return s, reject("invalid_sample", "Telemetry measurement is outside its supported range")
			}
			if v == 0 {
				*bound.value = 0
			} // Canonicalize negative zero for stable retry hashes.
		}
	}
	if (s.Latitude == nil) != (s.Longitude == nil) {
		return s, reject("invalid_sample", "latitude and longitude must be supplied together")
	}
	if len(s.FaultFlags) > 32 {
		return s, reject("invalid_sample", "At most 32 fault flags are supported")
	}
	flags := make(map[string]bool)
	s.FaultFlags = make([]string, 0, len(*wire.FaultFlags))
	for _, flag := range *wire.FaultFlags {
		if !flagPattern.MatchString(flag) {
			return s, reject("invalid_sample", "Invalid fault flag")
		}
		if !flags[flag] {
			flags[flag] = true
			s.FaultFlags = append(s.FaultFlags, flag)
		}
	}
	sort.Strings(s.FaultFlags)
	return s, nil
}
func fingerprint(s Sample) ([]byte, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	return hash[:], nil
}
