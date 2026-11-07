package telemetry

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
)

var ErrCycleConflict = errors.New("battery cycle reading changed; reload before recording")
var ErrCyclePermission = errors.New("an active fleet manager or maintenance technician is required")
var ErrInvalidCycleReading = errors.New("invalid cycle reading")

const MaxManualCycles int64 = 1000000000

type CycleBaseline struct {
	Total      int64      `json:"total"`
	Source     string     `json:"source"`
	RecordedAt *time.Time `json:"recorded_at"`
	Revision   int64      `json:"revision"`
	Note       string     `json:"note"`
}
type CycleReading struct {
	Total            int64  `json:"total"`
	ExpectedTotal    int64  `json:"expected_total"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func (r *CycleReading) Validate() error {
	r.Reason = strings.TrimSpace(r.Reason)
	if r.Total < 0 || r.Total > MaxManualCycles || r.ExpectedTotal < 0 || r.ExpectedRevision < 0 || !utf8.ValidString(r.Reason) || utf8.RuneCountInString(r.Reason) < 5 || utf8.RuneCountInString(r.Reason) > 500 {
		return ErrInvalidCycleReading
	}
	for _, c := range r.Reason {
		if unicode.IsControl(c) {
			return ErrInvalidCycleReading
		}
	}
	return nil
}

type CycleBaselineStore interface {
	ReadCycleBaseline(context.Context, string, string) (CycleBaseline, error)
	RecordCycleBaseline(context.Context, string, string, string, CycleReading) (CycleBaseline, error)
}

func (s *PostgresStore) ReadCycleBaseline(ctx context.Context, org, id string) (CycleBaseline, error) {
	var b CycleBaseline
	err := s.Pool.QueryRow(ctx, `SELECT battery_cycles,battery_cycles_source,battery_cycles_recorded_at,cycle_baseline_revision,battery_cycles_note FROM drones WHERE organisation_id=$1 AND id=$2`, org, id).Scan(&b.Total, &b.Source, &b.RecordedAt, &b.Revision, &b.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}
func (s *PostgresStore) RecordCycleBaseline(ctx context.Context, org, actor, id string, input CycleReading) (CycleBaseline, error) {
	if err := input.Validate(); err != nil {
		return CycleBaseline{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return CycleBaseline{}, err
	}
	defer tx.Rollback(ctx)
	var before CycleBaseline
	err = tx.QueryRow(ctx, `SELECT battery_cycles,battery_cycles_source,battery_cycles_recorded_at,cycle_baseline_revision,battery_cycles_note FROM drones WHERE organisation_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&before.Total, &before.Source, &before.RecordedAt, &before.Revision, &before.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return CycleBaseline{}, ErrNotFound
	}
	if err != nil {
		return CycleBaseline{}, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT true FROM users WHERE organisation_id=$1 AND id=$2 AND is_active AND role IN ('fleet_manager','maintenance_technician') FOR SHARE`, org, actor).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return CycleBaseline{}, ErrCyclePermission
	}
	if err != nil {
		return CycleBaseline{}, err
	}
	if before.Total != input.ExpectedTotal || before.Revision != input.ExpectedRevision || input.Total < before.Total {
		return CycleBaseline{}, ErrCycleConflict
	}
	var recorded time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&recorded); err != nil {
		return CycleBaseline{}, err
	}
	recorded = recorded.UTC()
	// A source may have accepted observations up to five minutes in the future.
	// Do not place a contradictory lower manual reading before that known total.
	var conflict bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telemetry_samples WHERE organisation_id=$1 AND drone_id=$2 AND observed_at>$3 AND battery_cycles<$4)`, org, id, recorded, input.Total).Scan(&conflict); err != nil {
		return CycleBaseline{}, err
	}
	if conflict {
		return CycleBaseline{}, ErrCycleConflict
	}
	after := CycleBaseline{Total: input.Total, Source: "manual", RecordedAt: &recorded, Revision: before.Revision + 1, Note: input.Reason}
	if _, err = tx.Exec(ctx, `UPDATE drones SET battery_cycles=$3,battery_cycles_source='manual',battery_cycles_recorded_at=$4,cycle_baseline_revision=$5,battery_cycles_note=$6 WHERE organisation_id=$1 AND id=$2`, org, id, after.Total, recorded, after.Revision, after.Note); err != nil {
		return CycleBaseline{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO battery_cycle_readings(organisation_id,drone_id,revision,actor_id,total,reason,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, org, id, after.Revision, actor, input.Total, input.Reason, recorded); err != nil {
		return CycleBaseline{}, err
	}
	if err = (audit.PostgresRecorder{}).Record(ctx, tx, audit.Event{OrganisationID: org, ActorID: actor, ResourceType: "battery_cycles", ResourceID: id, Action: audit.Updated, Before: before, After: after}); err != nil {
		return CycleBaseline{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CycleBaseline{}, err
	}
	return after, nil
}
