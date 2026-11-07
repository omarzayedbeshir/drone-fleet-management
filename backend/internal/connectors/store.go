package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

var ErrNotFound = errors.New("drone not found")
var ErrInvalid = errors.New("profile is unavailable for this drone")
var ErrInactive = errors.New("connector binding is no longer active")

func validUUID(v string) bool { var u pgtype.UUID; return len(v) == 36 && u.Scan(v) == nil && u.Valid }

type Settings struct {
	ProfileID string `json:"profile_id"`
	Enabled   bool   `json:"enabled"`
}
type Connector struct {
	DroneID          string          `json:"drone_id"`
	ProfileID        string          `json:"profile_id"`
	Enabled          bool            `json:"enabled"`
	Generation       int64           `json:"generation"`
	State            string          `json:"state"`
	Health           adapters.Health `json:"health"`
	UpdatedAt        time.Time       `json:"updated_at"`
	RuntimeUpdatedAt *time.Time      `json:"runtime_updated_at"`
}
type View struct {
	Connector *Connector       `json:"connector"`
	Profiles  []ProfileSummary `json:"profiles"`
}
type Store interface {
	Get(context.Context, string, string) (View, error)
	Save(context.Context, string, string, string, Settings) (View, error)
}
type PostgresStore struct {
	Pool     *pgxpool.Pool
	Profiles Profiles
}

func (s *PostgresStore) Get(ctx context.Context, org, id string) (View, error) {
	var vendor string
	if err := s.Pool.QueryRow(ctx, `SELECT vendor FROM drones WHERE organisation_id=$1 AND id=$2`, org, id).Scan(&vendor); errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	} else if err != nil {
		return View{}, err
	}
	view := View{Profiles: []ProfileSummary{}}
	for _, p := range s.Profiles {
		if p.Allowed(org, vendor) {
			view.Profiles = append(view.Profiles, p.Summary())
		}
	}
	sort.Slice(view.Profiles, func(i, j int) bool { return view.Profiles[i].ID < view.Profiles[j].ID })
	var c Connector
	var health []byte
	err := s.Pool.QueryRow(ctx, `SELECT drone_id::text,profile_id,enabled,generation,state,health,updated_at,runtime_updated_at FROM drone_connectors WHERE organisation_id=$1 AND drone_id=$2`, org, id).Scan(&c.DroneID, &c.ProfileID, &c.Enabled, &c.Generation, &c.State, &health, &c.UpdatedAt, &c.RuntimeUpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return view, nil
	}
	if err != nil {
		return View{}, err
	}
	if err = json.Unmarshal(health, &c.Health); err != nil {
		return View{}, err
	}
	// Runtime snapshots are observations, not proof of a currently running worker.
	if c.Enabled && c.State != "completed" && c.State != "failed" && ((c.RuntimeUpdatedAt == nil && time.Since(c.UpdatedAt) > 10*time.Second) || (c.RuntimeUpdatedAt != nil && time.Since(*c.RuntimeUpdatedAt) > 10*time.Second)) {
		c.State = "stopped"
		c.Health.Connected = false
		c.Health.Ready = false
		c.Health.LastError = "Connector supervisor has not reported recently"
	}
	view.Connector = &c
	return view, nil
}
func (s *PostgresStore) Save(ctx context.Context, org, actor, id string, input Settings) (View, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback(ctx)
	var vendor string
	// Same lock order as ingestion and registry deletion: drone, then connector.
	err = tx.QueryRow(ctx, `SELECT vendor FROM drones WHERE organisation_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&vendor)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND organisation_id=$2 AND role='fleet_manager' AND is_active)`, actor, org).Scan(&active); err != nil {
		return View{}, err
	}
	if !active {
		return View{}, ErrInactive
	}
	var before *Settings
	var old Settings
	err = tx.QueryRow(ctx, `SELECT profile_id,enabled FROM drone_connectors WHERE organisation_id=$1 AND drone_id=$2 FOR UPDATE`, org, id).Scan(&old.ProfileID, &old.Enabled)
	if err == nil {
		before = &old
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return View{}, err
	}
	p, ok := s.Profiles[input.ProfileID]
	if (input.Enabled || before == nil || old.ProfileID != input.ProfileID) && (!ok || !p.Allowed(org, vendor)) {
		return View{}, ErrInvalid
	}
	state := "disabled"
	if input.Enabled {
		state = "pending"
	}
	_, err = tx.Exec(ctx, `INSERT INTO drone_connectors(organisation_id,drone_id,actor_id,profile_id,enabled,state)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(drone_id) DO UPDATE SET actor_id=EXCLUDED.actor_id,profile_id=EXCLUDED.profile_id,enabled=EXCLUDED.enabled,state=EXCLUDED.state,generation=drone_connectors.generation+1,health='{}',runtime_owner=NULL,runtime_updated_at=NULL,updated_at=clock_timestamp()`, org, id, actor, input.ProfileID, input.Enabled, state)
	if err != nil {
		return View{}, err
	}
	action := audit.Updated
	if before == nil {
		action = audit.Created
	}
	if err = (audit.PostgresRecorder{}).Record(ctx, tx, audit.Event{OrganisationID: org, ActorID: actor, ResourceType: "connector", ResourceID: id, Action: action, Before: before, After: input}); err != nil {
		return View{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return View{}, err
	}
	return s.Get(ctx, org, id)
}

// Binding is internal and never serialized to API clients.
type Binding struct {
	Org, Drone, Actor, Profile, Vendor, Serial, State string
	Generation, Flight, Cycles, BaselineRevision      int64
	Active                                            bool
}

func (s *PostgresStore) bindings(ctx context.Context) ([]Binding, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.organisation_id::text,c.drone_id::text,c.actor_id::text,c.profile_id,d.vendor,d.serial_number,c.state,c.generation,d.flight_time_seconds,d.battery_cycles,d.cycle_baseline_revision,COALESCE(u.is_active AND u.role='fleet_manager' AND u.organisation_id=c.organisation_id,false) FROM drone_connectors c JOIN drones d ON d.id=c.drone_id AND d.organisation_id=c.organisation_id LEFT JOIN users u ON u.id=c.actor_id WHERE c.enabled`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Binding
	for rows.Next() {
		var b Binding
		if err = rows.Scan(&b.Org, &b.Drone, &b.Actor, &b.Profile, &b.Vendor, &b.Serial, &b.State, &b.Generation, &b.Flight, &b.Cycles, &b.BaselineRevision, &b.Active); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *PostgresStore) claim(ctx context.Context, b Binding, owner string) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE drone_connectors SET runtime_owner=$4,state='starting',health='{}',runtime_updated_at=clock_timestamp() WHERE organisation_id=$1 AND drone_id=$2 AND generation=$3 AND enabled`, b.Org, b.Drone, b.Generation, owner)
	return tag.RowsAffected() == 1, err
}
func (s *PostgresStore) report(ctx context.Context, b Binding, owner, state string, h adapters.Health) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE drone_connectors SET state=$5,health=$6,runtime_updated_at=clock_timestamp() WHERE organisation_id=$1 AND drone_id=$2 AND generation=$3 AND runtime_owner=$4 AND enabled`, b.Org, b.Drone, b.Generation, owner, state, raw)
	return err
}

// persist fences old workers and revoked actors in the same transaction as
// telemetry. Disabling/updating settings waits for in-flight writes, then prevents
// further writes by that generation, even before the supervisor cancels it.
func (s *PostgresStore) persist(ctx context.Context, b Binding, owner string, samples []telemetry.Sample) ([]error, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var vendor, serial string
	var baselineRevision int64
	err = tx.QueryRow(ctx, `SELECT vendor,serial_number,cycle_baseline_revision FROM drones WHERE organisation_id=$1 AND id=$2 FOR UPDATE`, b.Org, b.Drone).Scan(&vendor, &serial, &baselineRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &adapters.PermanentError{Err: ErrInactive}
	}
	if err != nil {
		return nil, err
	}
	if vendor != b.Vendor || serial != b.Serial || baselineRevision != b.BaselineRevision {
		return nil, &adapters.PermanentError{Err: ErrInactive}
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT true FROM drone_connectors c JOIN users u ON u.id=c.actor_id AND u.organisation_id=c.organisation_id WHERE c.organisation_id=$1 AND c.drone_id=$2 AND c.generation=$3 AND c.runtime_owner=$4 AND c.enabled AND u.is_active AND u.role='fleet_manager' FOR SHARE OF c,u`, b.Org, b.Drone, b.Generation, owner).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &adapters.PermanentError{Err: ErrInactive}
	}
	if err != nil {
		return nil, err
	}
	raws := make([][]byte, len(samples))
	for i, sample := range samples {
		raws[i], err = json.Marshal(sample)
		if err != nil {
			return nil, &adapters.PermanentError{Err: errors.New("invalid normalized telemetry")}
		}
	}
	items, err := (&telemetry.PostgresStore{Pool: s.Pool}).IngestBatchTx(ctx, tx, b.Org, b.Actor, b.Drone, raws)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := make([]error, len(items))
	for i, item := range items {
		if item.Rejection != nil {
			out[i] = item.Rejection
		}
	}
	return out, nil
}
