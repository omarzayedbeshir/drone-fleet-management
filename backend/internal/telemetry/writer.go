package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// NewTelemetryChannel is a transactionally published PostgreSQL notification.
// Consumers must scope organisation IDs and resync history after reconnecting.
const NewTelemetryChannel = "new_telemetry"

type Event struct {
	OrganisationID string    `json:"organisation_id"`
	DroneID        string    `json:"drone_id"`
	Source         string    `json:"source"`
	EventID        string    `json:"event_id"`
	ObservedAt     time.Time `json:"observed_at"`
	LatestUpdated  bool      `json:"latest_updated"`
}

func (s *PostgresStore) Ingest(ctx context.Context, org, actor, id string, raw []byte) (Result, error) {
	items, err := s.IngestBatch(ctx, org, actor, id, [][]byte{raw})
	if err != nil {
		return Result{}, err
	}
	if items[0].Rejection != nil {
		return Result{}, items[0].Rejection
	}
	return *items[0].Result, nil
}

// IngestBatch writes one drone's bounded batch with two COPY operations. The
// drone lock serializes API and adapter processes. Validation uses input order,
// checking pending observations as well as both stored temporal neighbours.
func (s *PostgresStore) IngestBatch(ctx context.Context, org, actor, id string, raws [][]byte) ([]BatchItem, error) {
	if len(raws) < 1 || len(raws) > MaxBatch {
		return nil, reject("invalid_sample", "Provide 1–100 telemetry records")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	items, err := s.IngestBatchTx(ctx, tx, org, actor, id, raws)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// IngestBatchTx joins a caller-owned transaction. The caller must commit or roll
// back; validation, receipts, projection and notifications remain atomic.
func (s *PostgresStore) IngestBatchTx(ctx context.Context, tx pgx.Tx, org, actor, id string, raws [][]byte) ([]BatchItem, error) {
	if len(raws) < 1 || len(raws) > MaxBatch {
		return nil, reject("invalid_sample", "Provide 1–100 telemetry records")
	}
	var err error
	var vendor, status string
	var latest *time.Time
	var flight, cycles int64
	err = tx.QueryRow(ctx, `SELECT vendor,status,last_telemetry_at,flight_time_seconds,battery_cycles
 FROM drones WHERE organisation_id=$1 AND id=$2 FOR UPDATE`, org, id).Scan(&vendor, &status, &latest, &flight, &cycles)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var received, cutoff time.Time
	err = tx.QueryRow(ctx, `SELECT clock_timestamp(),$1::timestamptz-retention FROM telemetry_storage_settings WHERE singleton`, s.now()).Scan(&received, &cutoff)
	if err != nil {
		return nil, err
	}
	received = received.UTC()
	var orgUUID, droneUUID, actorUUID pgtype.UUID
	if err := orgUUID.Scan(org); err != nil {
		return nil, err
	}
	if err := droneUUID.Scan(id); err != nil {
		return nil, err
	}
	if err := actorUUID.Scan(actor); err != nil {
		return nil, err
	}
	items := make([]BatchItem, len(raws))
	pending := make([]Stored, 0, len(raws))
	identities := make(map[string]int)
	var receipts, observations, rejections [][]any
	for i, raw := range raws {
		sample, cause := Decode(raw, s.now())
		var hash []byte
		if cause == nil {
			hash, err = fingerprint(sample)
			if err != nil {
				return nil, err
			}
			if sample.Source != "csv" && sample.Source != vendor {
				cause = reject("vendor_mismatch", "Telemetry source does not match the registered vendor")
			}
		}
		var duplicate *Stored
		if cause == nil {
			key := sample.Source + "/" + sample.EventID
			if index, ok := identities[key]; ok {
				previous := pending[index]
				previousHash, err := fingerprint(previous.Sample)
				if err != nil {
					return nil, err
				}
				if bytes.Equal(hash, previousHash) {
					duplicate = &previous
				} else {
					cause = reject("event_conflict", "event_id was already used with different telemetry")
				}
			} else {
				var previousHash []byte
				var previousReceived time.Time
				err := tx.QueryRow(ctx, `SELECT payload_hash,COALESCE(sample_received_at,received_at) FROM telemetry_receipts
 WHERE organisation_id=$1 AND drone_id=$2 AND source=$3 AND event_id=$4`, org, id, sample.Source, sample.EventID).Scan(&previousHash, &previousReceived)
				if err == nil {
					if bytes.Equal(hash, previousHash) {
						duplicate = &Stored{Sample: sample, ReceivedAt: previousReceived.UTC()}
					} else {
						cause = reject("event_conflict", "event_id was already used with different telemetry")
					}
				} else if !errors.Is(err, pgx.ErrNoRows) {
					return nil, err
				}
			}
		}
		if duplicate != nil {
			items[i].Result = &Result{Duplicate: true, Sample: *duplicate}
			continue
		}
		if cause == nil && sample.ObservedAt.Before(cutoff) {
			cause = reject("invalid_sample", "New observations must be within telemetry retention")
		}
		if cause == nil {
			cause = checkHistory(ctx, tx, org, id, sample, pending, latest, flight, cycles)
		}
		if cause != nil {
			var rejection *Rejection
			if !errors.As(cause, &rejection) {
				return nil, cause
			}
			digest := sha256.Sum256(raw)
			rejections = append(rejections, []any{orgUUID, droneUUID, actorUUID, rejection.Code, digest[:]})
			items[i].Rejection = rejection
			continue
		}
		stored := Stored{Sample: sample, ReceivedAt: received}
		updated := latest == nil || sample.ObservedAt.After(*latest)
		if updated {
			at := sample.ObservedAt
			latest, flight, cycles = &at, sample.FlightTimeSeconds, sample.BatteryCycles
			if status != "grounded" {
				status = sample.Status
			}
		}
		identities[sample.Source+"/"+sample.EventID] = len(pending)
		pending = append(pending, stored)
		receipts = append(receipts, []any{orgUUID, droneUUID, sample.Source, sample.EventID, sample.ObservedAt, hash, received})
		observations = append(observations, []any{orgUUID, droneUUID, sample.EventID, sample.Source, sample.ObservedAt, sample.Status,
			sample.FlightTimeSeconds, sample.BatteryCycles, sample.BatteryPercent, sample.Latitude, sample.Longitude, sample.AltitudeM, sample.SpeedMPS, sample.FaultFlags, received})
		items[i].Result = &Result{Sample: stored, LatestUpdated: updated}
	}
	for _, batch := range []struct {
		table   string
		columns []string
		rows    [][]any
	}{
		{"telemetry_rejections", []string{"organisation_id", "drone_id", "actor_id", "reason", "payload_hash"}, rejections},
		{"telemetry_receipts", []string{"organisation_id", "drone_id", "source", "event_id", "observed_at", "payload_hash", "received_at"}, receipts},
		{"telemetry_samples", []string{"organisation_id", "drone_id", "event_id", "source", "observed_at", "status", "flight_time_seconds", "battery_cycles", "battery_percent", "latitude", "longitude", "altitude_m", "speed_mps", "fault_flags", "received_at"}, observations},
	} {
		if len(batch.rows) == 0 {
			continue
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{batch.table}, batch.columns, pgx.CopyFromRows(batch.rows)); err != nil {
			return nil, err
		}
	}
	// Update once with the newest accepted observation, preserving grounded state.
	for i := len(pending) - 1; i >= 0; i-- {
		if pending[i].ObservedAt.Equal(*latest) {
			_, err := tx.Exec(ctx, `UPDATE drones SET status=$3,last_telemetry_at=$4,flight_time_seconds=$5,battery_cycles=$6,fault_flags=$7
 WHERE organisation_id=$1 AND id=$2`, org, id, status, latest, flight, cycles, pending[i].FaultFlags)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	for _, item := range items {
		if item.Result == nil || item.Result.Duplicate {
			continue
		}
		result := item.Result
		payload, err := json.Marshal(Event{org, id, result.Sample.Source, result.Sample.EventID, result.Sample.ObservedAt, result.LatestUpdated})
		if err != nil {
			return nil, err
		}
		// PostgreSQL delivers notifications only after this transaction commits.
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1,$2)`, NewTelemetryChannel, string(payload)); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func checkHistory(ctx context.Context, tx pgx.Tx, org, id string, sample Sample, pending []Stored, latest *time.Time, flight, cycles int64) error {
	var occupied bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telemetry_receipts WHERE organisation_id=$1 AND drone_id=$2 AND observed_at=$3)`, org, id, sample.ObservedAt).Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return reject("timestamp_conflict", "A different event already occupies this drone timestamp")
	}
	for _, previous := range pending {
		if previous.ObservedAt.Equal(sample.ObservedAt) {
			return reject("timestamp_conflict", "A different event already occupies this drone timestamp")
		}
		if contradicts(sample, previous.Sample) {
			return reject("counter_regression", "Cumulative counters contradict accepted telemetry history")
		}
	}
	// The projection survives retention, preserving the latest counter boundary.
	if latest != nil && contradicts(sample, Sample{ObservedAt: *latest, FlightTimeSeconds: flight, BatteryCycles: cycles}) {
		return reject("counter_regression", "Cumulative counters contradict the latest drone totals")
	}
	for _, query := range []string{
		`SELECT observed_at,flight_time_seconds,battery_cycles FROM telemetry_samples WHERE organisation_id=$1 AND drone_id=$2 AND observed_at<$3 ORDER BY observed_at DESC LIMIT 1`,
		`SELECT observed_at,flight_time_seconds,battery_cycles FROM telemetry_samples WHERE organisation_id=$1 AND drone_id=$2 AND observed_at>$3 ORDER BY observed_at LIMIT 1`,
	} {
		var previous Sample
		err := tx.QueryRow(ctx, query, org, id, sample.ObservedAt).Scan(&previous.ObservedAt, &previous.FlightTimeSeconds, &previous.BatteryCycles)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if contradicts(sample, previous) {
			return reject("counter_regression", "Cumulative counters contradict accepted telemetry history")
		}
	}
	return nil
}

func contradicts(sample, previous Sample) bool {
	if sample.ObservedAt.Before(previous.ObservedAt) {
		return sample.FlightTimeSeconds > previous.FlightTimeSeconds || sample.BatteryCycles > previous.BatteryCycles
	}
	return sample.FlightTimeSeconds < previous.FlightTimeSeconds || sample.BatteryCycles < previous.BatteryCycles
}
