package missions

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
)

type PostgresStore struct {
	Pool      *pgxpool.Pool
	Recorder  audit.Recorder
	Freshness time.Duration
}

const columns = `m.id::text, m.organisation_id::text, m.drone_id::text, m.pilot_id::text,
	m.location, m.starts_at, m.ends_at, m.near_populated_area, m.safety_rationale,
	m.safety_acknowledged, m.cancelled_at, m.created_at, m.updated_at`

func scan(row interface{ Scan(...any) error }, mission *Mission, extra ...any) error {
	err := row.Scan(append([]any{&mission.ID, &mission.OrganisationID, &mission.DroneID, &mission.PilotID,
		&mission.Location, &mission.StartsAt, &mission.EndsAt, &mission.NearPopulatedArea,
		&mission.SafetyRationale, &mission.SafetyAcknowledged, &mission.CancelledAt,
		&mission.CreatedAt, &mission.UpdatedAt}, extra...)...)
	mission.StartsAt, mission.EndsAt = mission.StartsAt.UTC(), mission.EndsAt.UTC()
	mission.CreatedAt, mission.UpdatedAt = mission.CreatedAt.UTC(), mission.UpdatedAt.UTC()
	if mission.CancelledAt != nil {
		cancelledAt := mission.CancelledAt.UTC()
		mission.CancelledAt = &cancelledAt
	}
	return translate(err)
}

func translate(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var dbErr *pgconn.PgError
	if errors.As(err, &dbErr) {
		switch dbErr.ConstraintName {
		case "missions_drone_overlap":
			return ErrDroneBusy
		case "missions_pilot_overlap":
			return ErrPilotBusy
		case "missions_drone_eligible":
			return ErrGrounded
		case "missions_pilot_eligible":
			return ErrPilotIneligible
		}
		if dbErr.Code == "23503" && dbErr.TableName == "missions" {
			return ErrAssignment
		}
	}
	return err
}

// Managers can see all assignments in their organisation. Pilots see only their own.
func pilotScope(user auth.User) *string {
	if user.Role == auth.Pilot {
		return &user.ID
	}
	return nil
}

func (s *PostgresStore) Options(ctx context.Context, user auth.User) (AssignmentOptions, error) {
	org := *user.OrganisationID
	drones, err := options(ctx, s.Pool, `SELECT id::text, name || ' (' || serial_number || ')', status <> 'grounded'
		FROM drones WHERE organisation_id=$1 ORDER BY name, serial_number, id`, org)
	if err != nil {
		return AssignmentOptions{}, err
	}
	pilots, err := options(ctx, s.Pool, `SELECT u.id::text, u.name, u.role = 'pilot' AND u.is_active
		FROM users u WHERE u.organisation_id=$1 AND ($2::uuid IS NULL OR u.id=$2)
		AND ((u.role='pilot' AND u.is_active) OR EXISTS (
			SELECT 1 FROM missions m WHERE m.organisation_id=$1 AND m.pilot_id=u.id
		)) ORDER BY u.name, u.id`, org, pilotScope(user))
	return AssignmentOptions{Drones: drones, Pilots: pilots}, err
}

func options(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) ([]AssignmentOption, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.AppendRows([]AssignmentOption{}, rows, pgx.RowToStructByPos[AssignmentOption])
}

func (s *PostgresStore) List(ctx context.Context, user auth.User, limit, offset int) ([]ListItem, error) {
	freshness := s.Freshness
	if freshness <= 0 {
		freshness = 30 * time.Second
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+columns+`,
		CASE WHEN d.status <> 'grounded' AND (d.last_telemetry_at IS NULL OR d.last_telemetry_at < $3) THEN 'unknown' ELSE d.status END
		FROM missions m JOIN drones d ON (d.organisation_id, d.id) = (m.organisation_id, m.drone_id)
		WHERE m.organisation_id = $1 AND ($2::uuid IS NULL OR m.pilot_id = $2)
		ORDER BY m.starts_at DESC, m.id DESC LIMIT $4 OFFSET $5`,
		*user.OrganisationID, pilotScope(user), time.Now().Add(-freshness), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ListItem{}
	for rows.Next() {
		var item ListItem
		if err = scan(rows, &item.Mission, &item.DroneStatus); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func lockMission(ctx context.Context, tx pgx.Tx, user auth.User, id string) (Mission, error) {
	var mission Mission
	err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM missions m
		WHERE m.organisation_id = $1 AND m.id = $2 AND ($3::uuid IS NULL OR m.pilot_id = $3)
		FOR UPDATE`, *user.OrganisationID, id, pilotScope(user)), &mission)
	return mission, err
}

func (s *PostgresStore) Save(ctx context.Context, user auth.User, id string, input Input) (Mission, error) {
	if user.Role == auth.Pilot && input.PilotID != user.ID {
		return Mission{}, ErrForbidden
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Mission{}, err
	}
	defer tx.Rollback(ctx)

	org := *user.OrganisationID
	args := []any{org, input.DroneID, input.PilotID, input.Location, input.StartsAt, input.EndsAt,
		input.NearPopulatedArea, input.SafetyRationale, input.SafetyAcknowledged}
	var before *Mission
	var after Mission
	action := audit.Created
	if id == "" {
		err = scan(tx.QueryRow(ctx, `INSERT INTO missions AS m
			(organisation_id, drone_id, pilot_id, location, starts_at, ends_at,
			 near_populated_area, safety_rationale, safety_acknowledged)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING `+columns, args...), &after)
	} else {
		existing, lockErr := lockMission(ctx, tx, user, id)
		if lockErr != nil {
			return Mission{}, lockErr
		}
		if existing.CancelledAt != nil {
			return Mission{}, ErrCancelled
		}
		before, action = &existing, audit.Updated
		args = append(args, id)
		err = scan(tx.QueryRow(ctx, `UPDATE missions AS m SET
			drone_id=$2, pilot_id=$3, location=$4, starts_at=$5, ends_at=$6,
			near_populated_area=$7, safety_rationale=$8, safety_acknowledged=$9
			WHERE organisation_id=$1 AND id=$10 RETURNING `+columns, args...), &after)
	}
	if err != nil {
		return Mission{}, err
	}
	if err = s.record(ctx, tx, user, action, before, after); err != nil {
		return Mission{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Mission{}, translate(err)
	}
	return after, nil
}

func (s *PostgresStore) Cancel(ctx context.Context, user auth.User, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	before, err := lockMission(ctx, tx, user, id)
	if err != nil || before.CancelledAt != nil {
		return err
	}
	var after Mission
	err = scan(tx.QueryRow(ctx, `UPDATE missions AS m SET cancelled_at=clock_timestamp()
		WHERE organisation_id=$1 AND id=$2 RETURNING `+columns, *user.OrganisationID, id), &after)
	if err != nil {
		return err
	}
	if err = s.record(ctx, tx, user, "cancelled", &before, after); err != nil {
		return err
	}
	return translate(tx.Commit(ctx))
}

func (s *PostgresStore) record(ctx context.Context, tx pgx.Tx, user auth.User, action audit.Action, before *Mission, after Mission) error {
	if s.Recorder == nil {
		return errors.New("mission scheduling requires an audit recorder")
	}
	return s.Recorder.Record(ctx, tx, audit.Event{
		OrganisationID: after.OrganisationID, ActorID: user.ID, ResourceType: "mission",
		ResourceID: after.ID, Action: action, Before: before, After: after,
	})
}
