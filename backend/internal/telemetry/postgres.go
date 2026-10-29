package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
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
func (s *PostgresStore) Ingest(ctx context.Context, org, actor, id string, raw []byte) (Result, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	var vendor, status string
	var latest *time.Time
	// One writer per drone makes deduplication, monotonic history, and latest
	// projection decisions deterministic even across multiple API processes.
	err = tx.QueryRow(ctx, `SELECT vendor,status,last_telemetry_at FROM drones WHERE organisation_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&vendor, &status, &latest)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, err
	}
	rejected := func(cause error) (Result, error) {
		var rejection *Rejection
		if !errors.As(cause, &rejection) {
			return Result{}, cause
		}
		digest := sha256.Sum256(raw)
		if _, err := tx.Exec(ctx, `INSERT INTO telemetry_rejections(organisation_id,drone_id,actor_id,reason,payload_hash) VALUES($1,$2,$3,$4,$5)`, org, id, actor, rejection.Code, digest[:]); err != nil {
			return Result{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, err
		}
		return Result{}, cause
	}
	sample, err := Decode(raw, s.now())
	if err != nil {
		return rejected(err)
	}
	if sample.Source != "csv" && sample.Source != vendor {
		return rejected(reject("vendor_mismatch", "Telemetry source does not match the registered vendor"))
	}
	hash, err := fingerprint(sample)
	if err != nil {
		return Result{}, err
	}
	var existingHash []byte
	var existingAt time.Time
	err = tx.QueryRow(ctx, `SELECT payload_hash,observed_at FROM telemetry_receipts WHERE organisation_id=$1 AND drone_id=$2 AND source=$3 AND event_id=$4`, org, id, sample.Source, sample.EventID).Scan(&existingHash, &existingAt)
	if err == nil {
		if !bytes.Equal(hash, existingHash) {
			return rejected(reject("event_conflict", "event_id was already used with different telemetry"))
		}
		stored, err := scan(tx.QueryRow(ctx, `SELECT `+sampleColumns+` FROM telemetry_samples WHERE organisation_id=$1 AND drone_id=$2 AND observed_at=$3`, org, id, existingAt))
		if err != nil {
			return Result{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, err
		}
		return Result{Duplicate: true, Sample: stored}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	var occupied bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telemetry_samples WHERE organisation_id=$1 AND drone_id=$2 AND observed_at=$3)`, org, id, sample.ObservedAt).Scan(&occupied); err != nil {
		return Result{}, err
	}
	if occupied {
		return rejected(reject("timestamp_conflict", "A different event already occupies this drone timestamp"))
	}
	// Check both neighbours: a delayed observation must not contradict either
	// earlier or later cumulative totals in the accepted history.
	for _, bound := range []struct {
		query   string
		earlier bool
	}{
		{`SELECT flight_time_seconds,battery_cycles FROM telemetry_samples WHERE organisation_id=$1 AND drone_id=$2 AND observed_at<$3 ORDER BY observed_at DESC LIMIT 1`, true},
		{`SELECT flight_time_seconds,battery_cycles FROM telemetry_samples WHERE organisation_id=$1 AND drone_id=$2 AND observed_at>$3 ORDER BY observed_at LIMIT 1`, false},
	} {
		var seconds, cycles int64
		err := tx.QueryRow(ctx, bound.query, org, id, sample.ObservedAt).Scan(&seconds, &cycles)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return Result{}, err
		}
		if (bound.earlier && (sample.FlightTimeSeconds < seconds || sample.BatteryCycles < cycles)) || (!bound.earlier && (sample.FlightTimeSeconds > seconds || sample.BatteryCycles > cycles)) {
			return rejected(reject("counter_regression", "Cumulative counters contradict accepted telemetry history"))
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO telemetry_receipts(organisation_id,drone_id,source,event_id,observed_at,payload_hash) VALUES($1,$2,$3,$4,$5,$6)`, org, id, sample.Source, sample.EventID, sample.ObservedAt, hash)
	if err != nil {
		return Result{}, err
	}
	stored, err := scan(tx.QueryRow(ctx, `INSERT INTO telemetry_samples
 (organisation_id,drone_id,event_id,source,observed_at,status,flight_time_seconds,battery_cycles,battery_percent,latitude,longitude,altitude_m,speed_mps,fault_flags)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+sampleColumns,
		org, id, sample.EventID, sample.Source, sample.ObservedAt, sample.Status, sample.FlightTimeSeconds, sample.BatteryCycles, sample.BatteryPercent, sample.Latitude, sample.Longitude, sample.AltitudeM, sample.SpeedMPS, sample.FaultFlags))
	if err != nil {
		return Result{}, err
	}
	updated := latest == nil || sample.ObservedAt.After(*latest)
	if updated {
		projected := sample.Status
		if status == "grounded" {
			projected = "grounded"
		}
		_, err = tx.Exec(ctx, `UPDATE drones SET status=$3,last_telemetry_at=$4,flight_time_seconds=$5,battery_cycles=$6,fault_flags=$7 WHERE organisation_id=$1 AND id=$2`, org, id, projected, sample.ObservedAt, sample.FlightTimeSeconds, sample.BatteryCycles, sample.FaultFlags)
		if err != nil {
			return Result{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return Result{Sample: stored, LatestUpdated: updated}, nil
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
