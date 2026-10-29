package ardupilot

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/bluenviron/gomavlib/v4/pkg/dialects/common"
	"github.com/bluenviron/gomavlib/v4/pkg/message"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

var fixtureNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func ready(t *testing.T) *normalizer {
	t.Helper()
	a, err := New(Config{FlightBaseline: 100, BatteryCycleBaseline: 7})
	if err != nil {
		t.Fatal(err)
	}
	n := newNormalizer(a.config)
	for _, msg := range []message.Message{
		&common.MessageHeartbeat{Autopilot: common.MAV_AUTOPILOT_ARDUPILOTMEGA, Type: common.MAV_TYPE_QUADROTOR, SystemStatus: common.MAV_STATE_STANDBY},
		&common.MessageSysStatus{BatteryRemaining: 85}, &common.MessageGpsRawInt{FixType: common.GPS_FIX_TYPE_3D_FIX},
		&common.MessageParamValue{ParamId: "STAT_BOOTCNT", ParamValue: 2}, &common.MessageParamValue{ParamId: "STAT_RESET", ParamValue: 1}, &common.MessageParamValue{ParamId: "STAT_FLTTIME", ParamValue: 120},
		&common.MessageSystemTime{TimeUnixUsec: uint64(fixtureNow.UnixMicro()), TimeBootMs: 10000},
	} {
		if _, err := n.handle(msg, fixtureNow); err != nil {
			t.Fatal(err)
		}
	}
	return n
}
func position(ms uint32) *common.MessageGlobalPositionInt {
	return &common.MessageGlobalPositionInt{TimeBootMs: ms, Lat: 300444000, Lon: 312357000, Alt: 123450, RelativeAlt: 23450, Vx: 300, Vy: 400}
}
func TestNormalizeUnitsAndCounters(t *testing.T) {
	n := ready(t)
	s, err := n.handle(position(10000), fixtureNow)
	if err != nil || s == nil {
		t.Fatal("missing sample", err)
	}
	if s.Source != "ardupilot" || s.Status != "available" || s.FlightTimeSeconds != 120 || s.BatteryCycles != 7 || *s.Latitude != 30.0444 || *s.Longitude != 31.2357 || *s.AltitudeM != 123.45 || *s.SpeedMPS != 5 || *s.BatteryPercent != 85 {
		t.Fatalf("incorrect normalization: %+v", s)
	}
	raw, _ := json.Marshal(s)
	if _, err := telemetry.Decode(raw, fixtureNow); err != nil {
		t.Fatal(err)
	}
	if duplicate, _ := n.handle(position(10000), fixtureNow); duplicate != nil {
		t.Fatal("duplicate position emitted twice")
	}
	if fast, _ := n.handle(position(10500), fixtureNow.Add(500*time.Millisecond)); fast != nil {
		t.Fatal("sampling interval not applied")
	}
}
func TestStalenessAndUnknownValues(t *testing.T) {
	n := ready(t)
	if s, _ := n.handle(position(20000), fixtureNow.Add(10*time.Second)); s != nil {
		t.Fatal("stale heartbeat/health emitted")
	}
	n = ready(t)
	n.gps.FixType = common.GPS_FIX_TYPE_NO_FIX
	n.battery = nil
	s, err := n.handle(position(10000), fixtureNow)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	if s.Latitude != nil || s.Longitude != nil || s.AltitudeM != nil || s.BatteryPercent != nil {
		t.Fatal("unknown measurements were fabricated")
	}
	n = ready(t)
	delete(n.paramTimes, "STAT_FLTTIME")
	if s, _ := n.handle(position(10000), fixtureNow); s != nil {
		t.Fatal("fabricated missing lifetime flight counter")
	}
	n = ready(t)
	n.params["STAT_FLTTIME"] = 99
	if _, err := n.handle(position(10000), fixtureNow); err == nil {
		t.Fatal("reset flight total accepted below baseline")
	}
	n = ready(t)
	if s, _ := n.handle(position(1), fixtureNow); s != nil {
		t.Fatal("old position relabelled with a fresh receive time")
	}
}
func TestConservativeStatusAndFaults(t *testing.T) {
	n := ready(t)
	n.heartbeat.BaseMode = common.MAV_MODE_FLAG_SAFETY_ARMED
	s, _ := n.handle(position(10000), fixtureNow)
	if s.Status != "unknown" {
		t.Fatal("arming alone was treated as flight")
	}
	n = ready(t)
	n.heartbeat.BaseMode = common.MAV_MODE_FLAG_SAFETY_ARMED
	n.landed = &common.MessageExtendedSysState{LandedState: common.MAV_LANDED_STATE_IN_AIR}
	n.landedAt = fixtureNow
	s, _ = n.handle(position(10000), fixtureNow)
	if s.Status != "in_mission" {
		t.Fatal("in-air state not mapped")
	}
	n = ready(t)
	n.heartbeat.SystemStatus = common.MAV_STATE_CRITICAL
	s, _ = n.handle(position(10000), fixtureNow)
	if s.Status != "grounded" || len(s.FaultFlags) != 1 {
		t.Fatal("critical state not grounded")
	}
	n = ready(t)
	n.sensors.OnboardControlSensorsPresent = 1
	n.sensors.OnboardControlSensorsEnabled = 1
	s, _ = n.handle(position(10000), fixtureNow)
	if s.Status != "unknown" || len(s.FaultFlags) != 1 {
		t.Fatal("unhealthy enabled sensor ignored")
	}
}
func TestRebootAndPrivacyBoundary(t *testing.T) {
	n := ready(t)
	first, _ := n.handle(position(10000), fixtureNow)
	if s, err := n.handle(&common.MessageStatustext{Text: "PRIVATE PAYLOAD"}, fixtureNow); s != nil || err != nil {
		t.Fatal("unmapped text crossed the boundary")
	}
	if _, err := n.handle(&common.MessageParamValue{ParamId: "STAT_FLTTIME", ParamValue: float32(math.NaN())}, fixtureNow); err == nil {
		t.Fatal("invalid parameter accepted")
	}
	if _, err := n.handle(&common.MessageParamValue{ParamId: "STAT_BOOTCNT", ParamValue: 3}, fixtureNow); err != nil {
		t.Fatal(err)
	}
	n.handle(&common.MessageParamValue{ParamId: "STAT_RESET", ParamValue: 1}, fixtureNow)
	n.handle(&common.MessageParamValue{ParamId: "STAT_FLTTIME", ParamValue: 120}, fixtureNow)
	n.handle(&common.MessageSystemTime{TimeUnixUsec: uint64(fixtureNow.Add(time.Second).UnixMicro()), TimeBootMs: 1000}, fixtureNow.Add(time.Second))
	second, err := n.handle(position(1000), fixtureNow.Add(time.Second))
	if err != nil || second == nil || second.EventID == first.EventID {
		t.Fatal("reboot identity/clock was not reset", err)
	}
}
func TestDeliveryRetriesSameObservation(t *testing.T) {
	a, err := New(Config{RetryMin: time.Millisecond, RetryMax: 4 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := make(chan telemetry.Sample, 1)
	queue <- telemetry.Sample{EventID: "stable-event"}
	calls := 0
	a.deliver(ctx, queue, func(_ context.Context, s telemetry.Sample) error {
		calls++
		if s.EventID != "stable-event" {
			t.Fatal("retry changed event identity")
		}
		if calls < 3 {
			return errors.New("temporary outage")
		}
		cancel()
		return nil
	})
	if calls != 3 || a.Health().Accepted != 1 {
		t.Fatal("retry/accept accounting failed")
	}
}
func TestDeliveryDoesNotRetryPermanentRejections(t *testing.T) {
	a, _ := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := make(chan telemetry.Sample, 2)
	queue <- telemetry.Sample{EventID: "invalid"}
	queue <- telemetry.Sample{EventID: "valid"}
	calls := 0
	a.deliver(ctx, queue, func(_ context.Context, s telemetry.Sample) error {
		calls++
		if s.EventID == "invalid" {
			return &adapters.PermanentError{Err: errors.New("invalid actor")}
		}
		cancel()
		return nil
	})
	if calls != 2 || a.Health().Rejected != 1 || a.Health().Accepted != 1 {
		t.Fatal("permanent rejection was retried or stopped future observations")
	}
}
func TestInvalidConfig(t *testing.T) {
	for _, config := range []Config{{Endpoint: "http://localhost:1234"}, {Endpoint: "udp://:14550"}, {Endpoint: "tcp://user:pass@localhost:5760"}, {Endpoint: "tcp://localhost:0"}, {QueueSize: -1}, {SystemID: 255}, {FlightBaseline: -1}} {
		if _, err := New(config); err == nil {
			t.Fatalf("accepted invalid configuration %+v", config)
		}
	}
}

func TestBoundedBufferAndPrimaryBattery(t *testing.T) {
	a, _ := New(Config{QueueSize: 1})
	queue := make(chan telemetry.Sample, 1)
	a.enqueue(queue, telemetry.Sample{EventID: "first"})
	a.enqueue(queue, telemetry.Sample{EventID: "dropped"})
	if a.Health().Dropped != 1 || (<-queue).EventID != "first" {
		t.Fatal("buffer replaced queued data or failed to report overflow")
	}
	n := ready(t)
	n.handle(&common.MessageBatteryStatus{Id: 0, BatteryRemaining: 42}, fixtureNow)
	n.handle(&common.MessageSysStatus{BatteryRemaining: -1}, fixtureNow)
	sample, err := n.handle(position(10000), fixtureNow)
	if err != nil || sample == nil || sample.BatteryPercent == nil || *sample.BatteryPercent != 42 {
		t.Fatal("ambiguous SYS_STATUS overwrote the primary battery reading", err)
	}
}
func TestEventIdentityDoesNotDependOnReceiverClock(t *testing.T) {
	first, second := ready(t), ready(t)
	first.clock = nil
	second.clock = nil
	a, _ := first.handle(position(10000), fixtureNow)
	b, _ := second.handle(position(10000), fixtureNow.Add(time.Millisecond))
	if a == nil || b == nil || a.EventID != b.EventID {
		t.Fatal("event identity depended on receiver clock")
	}
}

func TestReconnectRequiresFreshSensorState(t *testing.T) {
	n := ready(t)
	n.resetLink()
	now := fixtureNow.Add(2 * time.Second)
	for _, msg := range []message.Message{
		&common.MessageHeartbeat{Autopilot: common.MAV_AUTOPILOT_ARDUPILOTMEGA, Type: common.MAV_TYPE_QUADROTOR, SystemStatus: common.MAV_STATE_STANDBY},
		&common.MessageParamValue{ParamId: "STAT_BOOTCNT", ParamValue: 2}, &common.MessageParamValue{ParamId: "STAT_RESET", ParamValue: 1}, &common.MessageParamValue{ParamId: "STAT_FLTTIME", ParamValue: 120},
	} {
		n.handle(msg, now)
	}
	if sample, err := n.handle(position(10000), now); err != nil || sample != nil {
		t.Fatal("stale sensor cache crossed reconnect", err)
	}
	n.handle(&common.MessageSysStatus{BatteryRemaining: -1}, now)
	sample, err := n.handle(position(10000), now)
	if err != nil || sample == nil || sample.Latitude != nil || sample.BatteryPercent != nil {
		t.Fatal("fresh sensor state did not recover with unknown optional values", err)
	}
}
