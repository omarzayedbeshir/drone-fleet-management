package flightsummary

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
)

type Delivery struct {
	Report      Report    `json:"report"`
	DeliveredAt time.Time `json:"delivered_at"`
}

type Schedule struct {
	Enabled bool      `json:"enabled"`
	Latest  *Delivery `json:"latest"`
}

type PostgresSchedule struct{ Pool *pgxpool.Pool }

func (s PostgresSchedule) Read(ctx context.Context, org string) (Schedule, error) {
	var schedule Schedule
	var report json.RawMessage
	var deliveredAt *time.Time
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(settings.enabled, false), delivery.report, delivery.delivered_at
		FROM organisations o
		LEFT JOIN flight_summary_settings settings ON settings.organisation_id = o.id
		LEFT JOIN LATERAL (
			SELECT report, delivered_at FROM flight_summary_deliveries
			WHERE organisation_id = o.id ORDER BY reporting_day DESC LIMIT 1
		) delivery ON true
		WHERE o.id = $1`, org).Scan(&schedule.Enabled, &report, &deliveredAt)
	if err != nil {
		return Schedule{}, err
	}
	if deliveredAt != nil {
		latest := Delivery{DeliveredAt: deliveredAt.UTC()}
		if err := json.Unmarshal(report, &latest.Report); err != nil {
			return Schedule{}, err
		}
		schedule.Latest = &latest
	}
	return schedule, nil
}

func (s PostgresSchedule) SetEnabled(ctx context.Context, org, actor string, enabled bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO flight_summary_settings (organisation_id) VALUES ($1)
		ON CONFLICT (organisation_id) DO NOTHING`, org); err != nil {
		return err
	}
	var before bool
	if err := tx.QueryRow(ctx, `SELECT enabled FROM flight_summary_settings WHERE organisation_id=$1 FOR UPDATE`, org).Scan(&before); err != nil {
		return err
	}
	if before == enabled {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE flight_summary_settings SET enabled=$2 WHERE organisation_id=$1`, org, enabled); err != nil {
		return err
	}
	if err := (audit.PostgresRecorder{}).Record(ctx, tx, audit.Event{
		OrganisationID: org, ActorID: actor, ResourceType: "flight_summary_settings", ResourceID: org,
		Action: audit.Updated, Before: map[string]bool{"enabled": before}, After: map[string]bool{"enabled": enabled},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeliverDue publishes the previous completed UTC day's report in the app.
// Organisations enabled after midnight still receive that report on the next sweep.
func (s PostgresSchedule) DeliverDue(ctx context.Context, service Service, now time.Time) error {
	day := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	rows, err := s.Pool.Query(ctx, `SELECT settings.organisation_id::text
		FROM flight_summary_settings settings
		WHERE settings.enabled AND NOT EXISTS (
			SELECT 1 FROM flight_summary_deliveries d
			WHERE d.organisation_id = settings.organisation_id AND d.reporting_day = $1
		) ORDER BY settings.organisation_id`, day)
	if err != nil {
		return err
	}
	orgs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}

	for _, org := range orgs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		deliveryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.deliver(deliveryCtx, service, org, day)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Error("Scheduled flight summary delivery failed", "organisation_id", org, "error", err)
		}
	}
	return nil
}

func (s PostgresSchedule) deliver(ctx context.Context, service Service, org string, day time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT enabled FROM flight_summary_settings
		WHERE organisation_id=$1 FOR UPDATE SKIP LOCKED`, org).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || !enabled {
		return err
	}
	var delivered bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM flight_summary_deliveries WHERE organisation_id=$1 AND reporting_day=$2
	)`, org, day).Scan(&delivered); err != nil || delivered {
		return err
	}
	service.Reader = PostgresReader{Pool: tx}
	report, err := service.Generate(ctx, org, day, true)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flight_summary_deliveries (organisation_id, reporting_day, report)
		VALUES ($1, $2, $3)`, org, day, report); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s PostgresSchedule) Run(ctx context.Context, service Service) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.DeliverDue(ctx, service, time.Now()); err != nil && ctx.Err() == nil {
			slog.Error("Scheduled flight summary sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
