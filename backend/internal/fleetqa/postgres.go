package fleetqa

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ Pool *pgxpool.Pool }

func (s PostgresStore) Sources(ctx context.Context, org, question string) ([]Source, error) {
	rows, err := s.Pool.Query(ctx, `WITH aircraft AS (
		SELECT 'aircraft' AS type, id::text AS id, jsonb_build_object(
			'name', name, 'vendor', vendor, 'model', model, 'last_recorded_status', status,
			'last_telemetry_at', last_telemetry_at, 'flight_time_seconds', flight_time_seconds,
			'battery_cycles', battery_cycles
		) AS data, row_number() OVER (ORDER BY strpos(lower($2), lower(name)) > 0 DESC, updated_at DESC, id) AS rank
		FROM drones WHERE organisation_id = $1
		ORDER BY rank LIMIT 20
	), bookings AS (
		SELECT 'mission' AS type, m.id::text AS id, jsonb_build_object(
			'aircraft_name', d.name, 'starts_at', m.starts_at, 'ends_at', m.ends_at,
			'cancelled_at', m.cancelled_at, 'near_populated_area', m.near_populated_area
		) AS data, row_number() OVER (ORDER BY strpos(lower($2), lower(d.name)) > 0 DESC, m.starts_at DESC, m.id) AS rank
		FROM missions m JOIN drones d ON (d.organisation_id, d.id) = (m.organisation_id, m.drone_id)
		WHERE m.organisation_id = $1
		ORDER BY rank LIMIT 20
	), activity AS (
		SELECT 'telemetry' AS type, d.id::text AS id, jsonb_build_object(
			'aircraft_name', d.name, 'observed_at', active.observed_at, 'observed_status', 'in_mission',
			'source', active.source, 'fault_count', cardinality(active.fault_flags),
			'next_observed_at', later.observed_at, 'next_observed_status', later.status
		) AS data, row_number() OVER (ORDER BY active.observed_at DESC, d.id) AS rank
		FROM drones d
		JOIN LATERAL (
			SELECT observed_at, source, fault_flags
			FROM telemetry_samples t
			WHERE t.organisation_id = d.organisation_id AND t.drone_id = d.id AND t.status = 'in_mission'
			ORDER BY observed_at DESC LIMIT 1
		) active ON true
		LEFT JOIN LATERAL (
			SELECT observed_at, status FROM telemetry_samples t
			WHERE t.organisation_id = d.organisation_id AND t.drone_id = d.id AND t.observed_at > active.observed_at
			ORDER BY observed_at LIMIT 1
		) later ON true
		WHERE d.organisation_id = $1
		ORDER BY rank LIMIT 10
	)
	SELECT type, id, data FROM (
		SELECT * FROM aircraft UNION ALL SELECT * FROM bookings UNION ALL SELECT * FROM activity
	) records ORDER BY type, rank`, org, question)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := []Source{}
	for rows.Next() {
		var source Source
		if err := rows.Scan(&source.Type, &source.ID, &source.Data); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (s PostgresStore) Reserve(ctx context.Context, org string, day time.Time) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `INSERT INTO fleet_qa_usage (organisation_id, usage_day, requests)
		VALUES ($1, $2, 1)
		ON CONFLICT (organisation_id, usage_day) DO UPDATE
		SET requests = fleet_qa_usage.requests + 1
		WHERE fleet_qa_usage.requests < $3`, org, day, DailyLimit)
	return tag.RowsAffected() == 1, err
}
