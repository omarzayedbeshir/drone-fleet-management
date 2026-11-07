package connectors

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/ardupilot"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/csvreplay"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/dji"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/external"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/credentials"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/ingestion"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

// One session-scoped leader lock supervises every managed connection, reserving
// one DB connection per API leader rather than one connection per aircraft.
const leaderLock int64 = 748291607

type Factory func(Binding, Profile) (adapters.VendorAdapter, error)
type Manager struct {
	Store   *PostgresStore
	Factory Factory
	Poll    time.Duration
}
type worker struct {
	binding Binding
	owner   string
	cancel  context.CancelFunc
	done    chan struct{}
	adapter adapters.VendorAdapter
	mu      sync.Mutex
	state   string
	health  adapters.Health
}

func (w *worker) snapshot() (string, adapters.Health) {
	w.mu.Lock()
	defer w.mu.Unlock()
	h := w.health
	if w.adapter != nil && w.state == "running" {
		h = w.adapter.Health()
		if h.LastError != "" && !h.Ready {
			return "retrying", h
		}
	}
	return w.state, h
}
func (w *worker) set(state string, h adapters.Health) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.state = state
	w.health = h
}
func (m *Manager) Run(ctx context.Context) {
	poll := m.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for ctx.Err() == nil {
		conn, err := m.Store.Pool.Acquire(ctx)
		if err != nil {
			if !pause(ctx, poll) {
				return
			}
			continue
		}
		var leader bool
		err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_schema()::text,$1))`, leaderLock).Scan(&leader)
		if err == nil && leader {
			slog.Info("Connector supervisor acquired leadership")
			m.serve(ctx, conn, poll)
			release, cancel := context.WithTimeout(context.Background(), time.Second)
			_, _ = conn.Exec(release, `SELECT pg_advisory_unlock(hashtextextended(current_schema()::text,$1))`, leaderLock)
			cancel()
		}
		conn.Release()
		if !pause(ctx, poll) {
			return
		}
	}
}
func (m *Manager) serve(ctx context.Context, leader *pgxpool.Conn, poll time.Duration) {
	workers := map[string]*worker{}
	stop := func(w *worker) { w.cancel(); <-w.done }
	defer func() {
		for _, w := range workers {
			w.cancel()
		}
		for _, w := range workers {
			<-w.done
		}
	}()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		// A broken leadership session stops every local worker before re-election.
		err := leader.Conn().Ping(attempt)
		var bindings []Binding
		if err == nil {
			bindings, err = m.Store.bindings(attempt)
		}
		cancel()
		if err != nil {
			return
		}
		wanted := map[string]Binding{}
		for _, b := range bindings {
			wanted[b.Drone] = b
		}
		for id, w := range workers {
			b, ok := wanted[id]
			if !ok || b.Generation != w.binding.Generation || b.Serial != w.binding.Serial || b.Vendor != w.binding.Vendor || !b.Active {
				stop(w)
				delete(workers, id)
			}
		}
		for _, b := range bindings {
			if _, ok := workers[b.Drone]; ok {
				continue
			}
			if b.State == "completed" || b.State == "failed" {
				continue
			}
			owner := newOwner()
			attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
			claimed, err := m.Store.claim(attempt, b, owner)
			cancel()
			if err != nil || !claimed {
				continue
			}
			if !b.Active {
				attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
				_ = m.Store.report(attempt, b, owner, "failed", adapters.Health{LastError: "The configured fleet manager is no longer active"})
				cancel()
				continue
			}
			workerCtx, stopWorker := context.WithCancel(ctx)
			w := &worker{binding: b, owner: owner, cancel: stopWorker, done: make(chan struct{}), state: "starting"}
			workers[b.Drone] = w
			go m.runWorker(workerCtx, w)
		}
		for _, w := range workers {
			state, h := w.snapshot()
			attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = m.Store.report(attempt, w.binding, w.owner, state, h)
			cancel()
			if err != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (m *Manager) runWorker(ctx context.Context, w *worker) {
	defer close(w.done)
	defer func() {
		if ctx.Err() != nil {
			_, h := w.snapshot()
			h.Connected = false
			h.Ready = false
			w.set("stopped", h)
			attempt, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = m.Store.report(attempt, w.binding, w.owner, "stopped", h)
			cancel()
		}
	}()
	p, ok := m.Store.Profiles[w.binding.Profile]
	if !ok || !p.Allowed(w.binding.Org, w.binding.Vendor) || !w.binding.Active {
		w.set("failed", adapters.Health{LastError: "Connection profile or manager binding is unavailable"})
		return
	}
	factory := m.Factory
	if factory == nil {
		factory = BuildAdapter
	}
	delay := 250 * time.Millisecond
	for ctx.Err() == nil {
		adapter, err := factory(w.binding, p)
		if err != nil {
			w.set("failed", adapters.Health{LastError: "Connection profile could not be initialized"})
			return
		}
		w.mu.Lock()
		w.adapter = adapter
		w.state = "running"
		w.mu.Unlock()
		emitBatch := func(ctx context.Context, samples []telemetry.Sample) ([]error, error) {
			if p.Simulated {
				for i := range samples {
					samples[i].Source = "csv"
				}
			}
			attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return m.Store.persist(attempt, w.binding, w.owner, samples)
		}
		if batch, ok := adapter.(interface {
			RunBatch(context.Context, func(context.Context, []telemetry.Sample) ([]error, error)) error
		}); ok {
			err = batch.RunBatch(ctx, emitBatch)
		} else {
			err = adapter.Run(ctx, func(ctx context.Context, s telemetry.Sample) error {
				out, err := emitBatch(ctx, []telemetry.Sample{s})
				if err != nil {
					return err
				}
				return out[0]
			})
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			h := adapter.Health()
			h.Connected = false
			h.Ready = false
			w.set("completed", h)
			return
		}
		if errors.Is(err, vendor.ErrPermanent) {
			h := adapter.Health()
			h.Connected = false
			h.Ready = false
			h.LastError = "Source configuration failed; update connection settings to retry"
			w.set("failed", h)
			return
		}
		h := adapter.Health()
		h.Connected = false
		h.Ready = false
		h.LastError = "Source unavailable; retrying connection"
		w.set("retrying", h)
		slog.Warn("Managed connector retrying", "drone_id", w.binding.Drone)
		if !pause(ctx, delay) {
			return
		}
		delay = min(delay*2, 10*time.Second)
	}
}
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func BuildAdapter(b Binding, p Profile) (adapters.VendorAdapter, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.Source == "external" {
		return external.New(external.Config{Executable: p.Executable, Arguments: p.Arguments, Vendor: p.Vendor, Serial: b.Serial, CredentialRef: p.CredentialRef, FlightBaseline: b.Flight, CycleBaseline: b.Cycles, QueueSize: p.QueueSize})
	}
	if p.Source == "ardupilot" {
		return ardupilot.New(ardupilot.Config{Endpoint: p.Endpoint, QueueSize: p.QueueSize, SystemID: p.SystemID, ComponentID: p.ComponentID, FlightBaseline: b.Flight, BatteryCycleBaseline: b.Cycles})
	}
	var source vendor.VendorAdapter
	var err error
	switch p.Source {
	case "csv":
		source, err = csvreplay.New(csvreplay.Config{Path: p.CSVPath, Vendor: p.Vendor, Speed: p.Speed})
	case "dji":
		source, err = dji.New(dji.Config{BaseURL: p.Endpoint, Credentials: credentials.EnvProvider{}, CredentialRef: p.CredentialRef, DeviceSerials: []string{b.Serial}})
	default:
		return nil, errors.New("unsupported source")
	}
	if err != nil {
		return nil, err
	}
	return ingestion.New(ingestion.Config{Source: source, Serial: b.Serial, Vendor: b.Vendor, Simulated: p.Simulated, Capacity: p.QueueSize})
}

func newOwner() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("runtime identity entropy unavailable")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
