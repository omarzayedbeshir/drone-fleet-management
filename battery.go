package telemetry

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
)

type BatteryRating struct {
	RatedCycles   *int64  `json:"rated_cycles"`
	AlertFraction float64 `json:"alert_fraction"`
}

func (r BatteryRating) Validate() error {
	if r.RatedCycles != nil && *r.RatedCycles <= 0 {
		return errors.New("rated_cycles must be positive or null when unknown")
	}
	if math.IsNaN(r.AlertFraction) || math.IsInf(r.AlertFraction, 0) || r.AlertFraction <= 0 || r.AlertFraction > 1 {
		return errors.New("alert_fraction must be greater than zero and at most one")
	}
	return nil
}

type BatteryRatingStore interface {
	ConfigureBattery(context.Context, string, string, string, BatteryRating) (BatteryRating, error)
}

func (s *PostgresStore) ConfigureBattery(ctx context.Context, org, actor, id string, rating BatteryRating) (BatteryRating, error) {
	if err := rating.Validate(); err != nil {
		return BatteryRating{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return BatteryRating{}, err
	}
	defer tx.Rollback(ctx)
	var before BatteryRating
	err = tx.QueryRow(ctx, `SELECT battery_rated_cycles,battery_alert_fraction FROM drones
 WHERE organisation_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&before.RatedCycles, &before.AlertFraction)
	if errors.Is(err, pgx.ErrNoRows) {
		return BatteryRating{}, ErrNotFound
	}
	if err != nil {
		return BatteryRating{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE drones SET battery_rated_cycles=$3,battery_alert_fraction=$4
 WHERE organisation_id=$1 AND id=$2`, org, id, rating.RatedCycles, rating.AlertFraction); err != nil {
		return BatteryRating{}, err
	}
	if err := (audit.PostgresRecorder{}).Record(ctx, tx, audit.Event{
		OrganisationID: org, ActorID: actor, ResourceType: audit.DroneResource, ResourceID: id,
		Action: audit.Updated, Before: before, After: rating,
	}); err != nil {
		return BatteryRating{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BatteryRating{}, err
	}
	return rating, nil
}
