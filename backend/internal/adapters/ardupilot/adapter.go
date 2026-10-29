// Package ardupilot maps MAVLink ArduPilot/SITL observations to common telemetry.
package ardupilot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gomavlib/v4"
	"github.com/bluenviron/gomavlib/v4/pkg/dialects/ardupilotmega"
	"github.com/bluenviron/gomavlib/v4/pkg/dialects/common"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

type Config struct {
	Endpoint             string
	SystemID             uint8
	ComponentID          uint8
	Interval             time.Duration
	StaleAfter           time.Duration
	ParameterMaxAge      time.Duration
	QueueSize            int
	RetryMin             time.Duration
	RetryMax             time.Duration
	FlightBaseline       int64
	BatteryCycleBaseline int64
}
type Adapter struct {
	config  Config
	mu      sync.Mutex
	health  adapters.Health
	running atomic.Bool
}

var _ adapters.VendorAdapter = (*Adapter)(nil)

func New(c Config) (*Adapter, error) {
	if c.Endpoint == "" {
		c.Endpoint = "tcp://127.0.0.1:5760"
	}
	if c.SystemID == 0 {
		c.SystemID = 1
	}
	if c.ComponentID == 0 {
		c.ComponentID = 1
	}
	if c.SystemID == 255 {
		return nil, errors.New("target system ID must differ from the adapter GCS system ID 255")
	}
	if c.Interval == 0 {
		c.Interval = time.Second
	}
	if c.StaleAfter == 0 {
		c.StaleAfter = 5 * time.Second
	}
	if c.ParameterMaxAge == 0 {
		c.ParameterMaxAge = 30 * time.Second
	}
	if c.QueueSize == 0 {
		c.QueueSize = 32
	}
	if c.RetryMin == 0 {
		c.RetryMin = 250 * time.Millisecond
	}
	if c.RetryMax == 0 {
		c.RetryMax = 10 * time.Second
	}
	if c.Interval < 100*time.Millisecond || c.Interval > time.Minute || c.StaleAfter < time.Second || c.ParameterMaxAge < 5*time.Second || c.QueueSize < 1 || c.QueueSize > 4096 || c.RetryMin <= 0 || c.RetryMax < c.RetryMin || c.FlightBaseline < 0 || c.BatteryCycleBaseline < 0 {
		return nil, errors.New("invalid adapter timing, buffer, or baseline configuration")
	}
	if _, err := endpoint(c.Endpoint); err != nil {
		return nil, err
	}
	return &Adapter{config: c, health: adapters.Health{LastError: "Waiting for ArduPilot heartbeat, statistics parameters, and telemetry"}}, nil
}
func endpoint(value string) (gomavlib.Endpoint, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("endpoint must be tcp://host:port or udp://listen-host:port")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" {
		return nil, errors.New("endpoint must include a host and port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, errors.New("invalid endpoint port")
	}
	switch u.Scheme {
	case "tcp":
		return &gomavlib.EndpointTCPClient{Address: u.Host}, nil
	case "udp":
		return &gomavlib.EndpointUDPServer{Address: u.Host}, nil
	}
	return nil, fmt.Errorf("unsupported MAVLink transport %q", u.Scheme)
}
func (a *Adapter) change(fn func(*adapters.Health)) { a.mu.Lock(); defer a.mu.Unlock(); fn(&a.health) }
func (a *Adapter) Health() adapters.Health {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.health
	h.Connected = a.running.Load() && !h.LastHeartbeat.IsZero() && time.Since(h.LastHeartbeat) <= a.config.StaleAfter
	h.Ready = h.Connected && !h.LastEmitted.IsZero() && time.Since(h.LastEmitted) <= max(a.config.StaleAfter, 2*a.config.Interval)
	return h
}
func (a *Adapter) Run(ctx context.Context, emit adapters.Emit) error {
	if emit == nil {
		return errors.New("an ingestion callback is required")
	}
	if !a.running.CompareAndSwap(false, true) {
		return errors.New("adapter is already running")
	}
	defer a.running.Store(false)
	ep, _ := endpoint(a.config.Endpoint)
	node := &gomavlib.Node{Endpoints: []gomavlib.Endpoint{ep}, Dialect: ardupilotmega.Dialect, OutVersion: gomavlib.V2, OutSystemID: 255, OutComponentID: 190, HeartbeatPeriod: time.Second, ReadTimeout: a.config.StaleAfter, WriteTimeout: 2 * time.Second, IdleTimeout: a.config.StaleAfter}
	if err := node.Initialize(); err != nil {
		return err
	}
	defer node.Close()
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := make(chan telemetry.Sample, a.config.QueueSize)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); a.deliver(workerCtx, queue, emit) }()
	defer func() { cancel(); <-workerDone }()
	normal := newNormalizer(a.config)
	poll := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	var channel *gomavlib.Channel
	requests := func() {
		if channel == nil {
			return
		}
		for _, name := range []string{"STAT_BOOTCNT", "STAT_RESET", "STAT_FLTTIME"} {
			_ = node.WriteMessageTo(channel, &common.MessageParamRequestRead{TargetSystem: a.config.SystemID, TargetComponent: a.config.ComponentID, ParamId: name, ParamIndex: -1})
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-poll.C:
			requests()
		case event, ok := <-node.Events():
			if !ok {
				return errors.New("MAVLink event stream closed")
			}
			switch event := event.(type) {
			case *gomavlib.EventChannelClose:
				if event.Channel == channel {
					channel = nil
					normal.resetLink()
					a.change(func(h *adapters.Health) {
						h.LastHeartbeat = time.Time{}
						h.LastError = "MAVLink connection lost; reconnecting"
					})
				}
			case *gomavlib.EventParseError:
				a.change(func(h *adapters.Health) { h.ParseErrors++; h.LastError = "Malformed MAVLink frame rejected" })
			case *gomavlib.EventFrame:
				if event.SystemID() != a.config.SystemID || event.ComponentID() != a.config.ComponentID {
					continue
				}
				msg := event.Message()
				heartbeat, isHeartbeat := msg.(*common.MessageHeartbeat)
				validHeartbeat := isHeartbeat && heartbeat.Autopilot == common.MAV_AUTOPILOT_ARDUPILOTMEGA && heartbeat.Type != common.MAV_TYPE_GCS
				if channel != nil && channel != event.Channel {
					continue
				}
				if channel == nil && !validHeartbeat {
					continue
				}
				now := time.Now()
				if heartbeat, ok := msg.(*common.MessageHeartbeat); ok && heartbeat.Autopilot == common.MAV_AUTOPILOT_ARDUPILOTMEGA && heartbeat.Type != common.MAV_TYPE_GCS {
					if channel != event.Channel {
						channel = event.Channel
						_ = node.WriteMessageTo(channel, &common.MessageRequestDataStream{TargetSystem: a.config.SystemID, TargetComponent: a.config.ComponentID, ReqStreamId: common.MAV_DATA_STREAM_ALL, ReqMessageRate: 4, StartStop: 1})
						// Telemetry message requests only; never arm, change mode, or actuate.
						for _, id := range []float32{2, 245} {
							_ = node.WriteMessageTo(channel, &common.MessageCommandLong{TargetSystem: a.config.SystemID, TargetComponent: a.config.ComponentID, Command: common.MAV_CMD_SET_MESSAGE_INTERVAL, Param1: id, Param2: 1000000})
						}
						requests()
					}
					a.change(func(h *adapters.Health) { h.LastHeartbeat = now })
				}
				a.change(func(h *adapters.Health) { h.LastFrame = now })
				sample, err := normal.handle(msg, now)
				if err != nil {
					a.change(func(h *adapters.Health) { h.LastError = err.Error() })
					continue
				}
				if sample != nil {
					a.enqueue(queue, *sample)
				}
			}
		}
	}
}
func (a *Adapter) deliver(ctx context.Context, queue <-chan telemetry.Sample, emit adapters.Emit) {
	for {
		var sample telemetry.Sample
		select {
		case <-ctx.Done():
			return
		case sample = <-queue:
		}
		delay := a.config.RetryMin
		for {
			err := emit(ctx, sample)
			if err == nil {
				a.change(func(h *adapters.Health) { h.Accepted++; h.LastEmitted = time.Now(); h.LastError = "" })
				break
			}
			if ctx.Err() != nil {
				return
			}
			var rejected *telemetry.Rejection
			var permanent *adapters.PermanentError
			if errors.As(err, &rejected) || errors.As(err, &permanent) || errors.Is(err, telemetry.ErrNotFound) {
				a.change(func(h *adapters.Health) { h.Rejected++; h.LastError = err.Error() })
				break
			}
			a.change(func(h *adapters.Health) { h.LastError = "Ingestion unavailable; retrying buffered observation" })
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if delay < a.config.RetryMax {
				delay *= 2
				if delay > a.config.RetryMax {
					delay = a.config.RetryMax
				}
			}
		}
	}
}

func (a *Adapter) enqueue(queue chan<- telemetry.Sample, sample telemetry.Sample) {
	select {
	case queue <- sample:
	default:
		a.change(func(h *adapters.Health) { h.Dropped++; h.LastError = "Telemetry queue full; new observation dropped" })
	}
}
