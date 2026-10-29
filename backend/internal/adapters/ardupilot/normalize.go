package ardupilot

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/bluenviron/gomavlib/v4/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v4/pkg/message"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

// normalizer is confined to the reader goroutine. Only these typed messages
// cross the privacy boundary; STATUSTEXT, images, and payload blobs are ignored.
type normalizer struct {
	config           Config
	heartbeat        *common.MessageHeartbeat
	heartbeatAt      time.Time
	sensors          *common.MessageSysStatus
	sensorsAt        time.Time
	gps              *common.MessageGpsRawInt
	gpsAt            time.Time
	landed           *common.MessageExtendedSysState
	landedAt         time.Time
	battery          *float64
	batteryAt        time.Time
	primaryBatteryAt time.Time
	clock            *common.MessageSystemTime
	clockAt          time.Time
	params           map[string]float64
	paramTimes       map[string]time.Time
	lastBoot         uint32
	lastOutput       bool
	lastObserved     time.Time
}

func newNormalizer(c Config) *normalizer {
	return &normalizer{config: c, params: make(map[string]float64), paramTimes: make(map[string]time.Time)}
}
func fresh(now, at time.Time, age time.Duration) bool {
	return !at.IsZero() && now.Sub(at) >= 0 && now.Sub(at) <= age
}
func (n *normalizer) resetLink() {
	n.heartbeat = nil
	n.heartbeatAt = time.Time{}
	n.sensors = nil
	n.gps = nil
	n.landed = nil
	n.clock = nil
	n.battery = nil
	n.sensorsAt = time.Time{}
	n.gpsAt = time.Time{}
	n.landedAt = time.Time{}
	n.clockAt = time.Time{}
	n.batteryAt = time.Time{}
	n.primaryBatteryAt = time.Time{}
	n.paramTimes = make(map[string]time.Time)
	// Retain the last position identity across reconnects to ignore replayed frames.
}
func (n *normalizer) handle(msg message.Message, now time.Time) (*telemetry.Sample, error) {
	switch m := msg.(type) {
	case *common.MessageHeartbeat:
		if m.Autopilot != common.MAV_AUTOPILOT_ARDUPILOTMEGA || m.Type == common.MAV_TYPE_GCS {
			return nil, errors.New("target is not an ArduPilot flight controller")
		}
		copy := *m
		n.heartbeat = &copy
		n.heartbeatAt = now
	case *common.MessageSysStatus:
		copy := *m
		n.sensors = &copy
		n.sensorsAt = now
		if !fresh(now, n.primaryBatteryAt, n.config.StaleAfter) {
			if m.BatteryRemaining >= 0 {
				value := float64(m.BatteryRemaining)
				n.battery = &value
			} else {
				n.battery = nil
			}
			n.batteryAt = now
		}
	case *common.MessageBatteryStatus:
		if m.Id == 0 {
			if m.BatteryRemaining >= 0 {
				value := float64(m.BatteryRemaining)
				n.battery = &value
			} else {
				n.battery = nil
			}
			n.batteryAt = now
			n.primaryBatteryAt = now
		}
	case *common.MessageGpsRawInt:
		copy := *m
		n.gps = &copy
		n.gpsAt = now
	case *common.MessageExtendedSysState:
		copy := *m
		n.landed = &copy
		n.landedAt = now
	case *common.MessageSystemTime:
		copy := *m
		n.clock = &copy
		n.clockAt = now
	case *common.MessageParamValue:
		name := strings.TrimRight(m.ParamId, "\x00")
		if name != "STAT_FLTTIME" && name != "STAT_BOOTCNT" && name != "STAT_RESET" {
			return nil, nil
		}
		value := float64(m.ParamValue)
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > math.MaxInt32 || value != math.Trunc(value) {
			return nil, errors.New("invalid ArduPilot statistics parameter")
		}
		if previous, exists := n.params[name]; name == "STAT_BOOTCNT" && exists && previous != value {
			n.lastOutput = false
			n.clock = nil
			n.gps = nil
			n.landed = nil
			delete(n.paramTimes, "STAT_FLTTIME")
			delete(n.paramTimes, "STAT_RESET")
		}
		n.params[name] = value
		n.paramTimes[name] = now
	case *common.MessageGlobalPositionInt:
		if n.heartbeat == nil || n.sensors == nil || !fresh(now, n.heartbeatAt, n.config.StaleAfter) || !fresh(now, n.sensorsAt, n.config.StaleAfter) {
			return nil, nil
		}
		for _, name := range []string{"STAT_BOOTCNT", "STAT_RESET", "STAT_FLTTIME"} {
			if !fresh(now, n.paramTimes[name], n.config.ParameterMaxAge) {
				return nil, nil
			}
		}
		if n.lastOutput {
			elapsed := int32(m.TimeBootMs - n.lastBoot)
			if elapsed <= 0 || time.Duration(elapsed)*time.Millisecond < n.config.Interval {
				return nil, nil
			}
		}
		flight := int64(n.params["STAT_FLTTIME"])
		if flight < n.config.FlightBaseline {
			return nil, errors.New("STAT_FLTTIME is below the stored lifetime total; reconcile a statistics reset before ingesting")
		}
		observed := now.UTC().Truncate(time.Microsecond)
		if n.clock != nil && fresh(now, n.clockAt, n.config.StaleAfter) && n.clock.TimeUnixUsec > 0 && n.clock.TimeUnixUsec <= math.MaxInt64 {
			anchor := time.UnixMicro(int64(n.clock.TimeUnixUsec)).UTC()
			if anchor.Sub(n.clockAt) <= n.config.StaleAfter && n.clockAt.Sub(anchor) <= n.config.StaleAfter {
				candidate := anchor.Add(time.Duration(int32(m.TimeBootMs-n.clock.TimeBootMs)) * time.Millisecond)
				// A synchronized clock identifies delayed/replayed positions:
				// do not relabel them as fresh receive-time observations.
				if candidate.Sub(now) > n.config.StaleAfter || now.Sub(candidate) > n.config.StaleAfter {
					return nil, nil
				}
				observed = candidate.Truncate(time.Microsecond)
			}
		}
		if n.lastOutput && !observed.After(n.lastObserved) {
			return nil, nil
		}
		sample := telemetry.Sample{Source: "ardupilot", ObservedAt: observed, Status: "unknown", FlightTimeSeconds: flight, BatteryCycles: n.config.BatteryCycleBaseline, FaultFlags: []string{}}
		failed := uint64(n.sensors.OnboardControlSensorsPresent & n.sensors.OnboardControlSensorsEnabled &^ n.sensors.OnboardControlSensorsHealth)
		if failed != 0 {
			sample.FaultFlags = append(sample.FaultFlags, fmt.Sprintf("SENSOR_UNHEALTHY_%08X", failed))
		}
		switch n.heartbeat.SystemStatus {
		case common.MAV_STATE_CRITICAL, common.MAV_STATE_EMERGENCY, common.MAV_STATE_FLIGHT_TERMINATION:
			sample.Status = "grounded"
			sample.FaultFlags = append(sample.FaultFlags, "AUTOPILOT_CRITICAL")
		case common.MAV_STATE_ACTIVE, common.MAV_STATE_STANDBY:
			armed := n.heartbeat.BaseMode&common.MAV_MODE_FLAG_SAFETY_ARMED != 0
			inAir := n.landed != nil && fresh(now, n.landedAt, n.config.StaleAfter) && (n.landed.LandedState == common.MAV_LANDED_STATE_IN_AIR || n.landed.LandedState == common.MAV_LANDED_STATE_TAKEOFF || n.landed.LandedState == common.MAV_LANDED_STATE_LANDING)
			if armed && inAir {
				sample.Status = "in_mission"
			} else if !armed && failed == 0 {
				sample.Status = "available"
			}
		}
		sort.Strings(sample.FaultFlags)
		if n.battery != nil && fresh(now, n.batteryAt, n.config.StaleAfter) {
			value := *n.battery
			sample.BatteryPercent = &value
		}
		if n.gps != nil && fresh(now, n.gpsAt, n.config.StaleAfter) && n.gps.FixType >= common.GPS_FIX_TYPE_3D_FIX {
			lat, lon, alt := float64(m.Lat)/1e7, float64(m.Lon)/1e7, float64(m.Alt)/1000
			speed := math.Sqrt(float64(m.Vx)*float64(m.Vx)+float64(m.Vy)*float64(m.Vy)+float64(m.Vz)*float64(m.Vz)) / 100
			sample.Latitude = &lat
			sample.Longitude = &lon
			sample.AltitudeM = &alt
			sample.SpeedMPS = &speed
		}
		sample.EventID = fmt.Sprintf("ap:%d:%d:%.0f:%.0f:%d", n.config.SystemID, n.config.ComponentID, n.params["STAT_RESET"], n.params["STAT_BOOTCNT"], m.TimeBootMs)
		n.lastBoot = m.TimeBootMs
		n.lastOutput = true
		n.lastObserved = observed
		return &sample, nil
	}
	return nil, nil
}
