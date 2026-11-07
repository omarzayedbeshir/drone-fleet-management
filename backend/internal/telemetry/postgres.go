package telemetry

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
}

func (s *PostgresStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

const sampleColumns = `event_id,source,observed_at,status,flight_time_seconds,battery_cycles,battery_percent,
 latitude,longitude,altitude_m,speed_mps,fault_flags,received_at`

func scan(row interface{ Scan(...any) error }) (Stored, error) {
	var s Stored
	err := row.Scan(&s.EventID, &s.Source, &s.ObservedAt, &s.Status, &s.FlightTimeSeconds, &s.BatteryCycles, &s.BatteryPercent,
		&s.Latitude, &s.Longitude, &s.AltitudeM, &s.SpeedMPS, &s.FaultFlags, &s.ReceivedAt)
	s.ObservedAt = s.ObservedAt.UTC()
	s.ReceivedAt = s.ReceivedAt.UTC()
	return s, err
}
func (s *PostgresStore) List(ctx context.Context, org, id string, limit, offset int) ([]Stored, bool, error) {
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM drones WHERE organisation_id=$1 AND id=$2)`, org, id).Scan(&exists); err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, ErrNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+sampleColumns+` FROM telemetry_samples WHERE organisation_id=$1 AND drone_id=$2 ORDER BY observed_at DESC LIMIT $3 OFFSET $4`, org, id, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]Stored, 0)
	for rows.Next() {
		sample, err := scan(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	return result, more, nil
}
