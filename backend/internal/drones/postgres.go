package drones

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ Pool *pgxpool.Pool }

const columns = `id::text, organisation_id::text, name, vendor, model, serial_number,
 status, last_telemetry_at, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanDrone(row scanner) (Drone, error) {
	var d Drone
	err := row.Scan(&d.ID, &d.OrganisationID, &d.Name, &d.Vendor, &d.Model, &d.SerialNumber,
		&d.Status, &d.LastTelemetryAt, &d.CreatedAt, &d.UpdatedAt)
	d.CreatedAt = d.CreatedAt.UTC()
	d.UpdatedAt = d.UpdatedAt.UTC()
	if d.LastTelemetryAt != nil {
		value := d.LastTelemetryAt.UTC()
		d.LastTelemetryAt = &value
	}
	return d, translateError(err)
}
func translateError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "23503") {
		return ErrConflict
	}
	return err
}

func recordEvent(ctx context.Context, tx pgx.Tx, org, actor, id, action string, before, after *Drone) error {
	var oldData, newData []byte
	var err error
	if before != nil {
		oldData, err = json.Marshal(before)
		if err != nil {
			return err
		}
	}
	if after != nil {
		newData, err = json.Marshal(after)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO drone_registry_events
  (organisation_id, actor_id, drone_id, action, before_data, after_data) VALUES ($1,$2,$3,$4,$5,$6)`,
		org, actor, id, action, oldData, newData)
	return err
}

func (s *PostgresStore) Create(ctx context.Context, org, actor string, m Metadata) (Drone, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Drone{}, err
	}
	defer tx.Rollback(ctx)
	d, err := scanDrone(tx.QueryRow(ctx, `INSERT INTO drones (organisation_id,name,vendor,model,serial_number)
  VALUES ($1,$2,$3,$4,$5) RETURNING `+columns, org, m.Name, m.Vendor, m.Model, m.SerialNumber))
	if err != nil {
		return Drone{}, err
	}
	if err := recordEvent(ctx, tx, org, actor, d.ID, "created", nil, &d); err != nil {
		return Drone{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Drone{}, translateError(err)
	}
	return d, nil
}

func (s *PostgresStore) List(ctx context.Context, org string, f Filter) ([]Drone, bool, error) {
	// Treat the search term literally, rather than allowing SQL wildcard expansion.
	query := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query)
	rows, err := s.Pool.Query(ctx, `SELECT `+columns+` FROM drones
  WHERE organisation_id=$1 AND ($2='' OR vendor=$2) AND ($3='' OR status=$3)
  AND ($4='' OR name ILIKE '%' || $4 || '%' OR model ILIKE '%' || $4 || '%' OR serial_number ILIKE '%' || $4 || '%')
  ORDER BY created_at DESC, id DESC LIMIT $5 OFFSET $6`, org, f.Vendor, f.Status, query, f.Limit+1, f.Offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]Drone, 0)
	for rows.Next() {
		d, err := scanDrone(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, d)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(result) > f.Limit
	if more {
		result = result[:f.Limit]
	}
	return result, more, nil
}

func (s *PostgresStore) Get(ctx context.Context, org, id string) (Drone, error) {
	return scanDrone(s.Pool.QueryRow(ctx, `SELECT `+columns+` FROM drones WHERE organisation_id=$1 AND id=$2`, org, id))
}

func (s *PostgresStore) Update(ctx context.Context, org, actor, id string, p Patch) (Drone, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Drone{}, err
	}
	defer tx.Rollback(ctx)
	before, err := scanDrone(tx.QueryRow(ctx, `SELECT `+columns+` FROM drones WHERE organisation_id=$1 AND id=$2 FOR UPDATE`, org, id))
	if err != nil {
		return Drone{}, err
	}
	m := p.Apply(before)
	after, err := scanDrone(tx.QueryRow(ctx, `UPDATE drones SET name=$3,vendor=$4,model=$5,serial_number=$6
  WHERE organisation_id=$1 AND id=$2 RETURNING `+columns, org, id, m.Name, m.Vendor, m.Model, m.SerialNumber))
	if err != nil {
		return Drone{}, err
	}
	if err := recordEvent(ctx, tx, org, actor, id, "updated", &before, &after); err != nil {
		return Drone{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Drone{}, translateError(err)
	}
	return after, nil
}

func (s *PostgresStore) Delete(ctx context.Context, org, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	before, err := scanDrone(tx.QueryRow(ctx, `DELETE FROM drones WHERE organisation_id=$1 AND id=$2 RETURNING `+columns, org, id))
	if err != nil {
		return err
	}
	if err := recordEvent(ctx, tx, org, actor, id, "deleted", &before, nil); err != nil {
		return err
	}
	return translateError(tx.Commit(ctx))
}
