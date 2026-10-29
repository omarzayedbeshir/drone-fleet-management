package httpapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
)

func TestAuditUpgradeIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t, "000004_drone_registry_audit.sql")
	var org, id string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Legacy tenant') RETURNING id::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var eventID int64
	var created time.Time
	if err := pool.QueryRow(ctx, `INSERT INTO drone_registry_events(organisation_id,actor_id,drone_id,action,after_data)
 VALUES($1,$2,$2,'created','{"name":"Legacy drone"}') RETURNING id,created_at`, org, id).Scan(&eventID, &created); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000005_shared_audit_events.sql"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var kind, resource, actor, action, name string
	var upgradedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT resource_type,resource_id::text,actor_id::text,action,after_data->>'name',created_at
 FROM audit_events WHERE id=$1`, eventID).Scan(&kind, &resource, &actor, &action, &name, &upgradedAt); err != nil {
		t.Fatal(err)
	}
	if kind != "drone" || resource != id || actor != id || action != "created" || name != "Legacy drone" || !created.Equal(upgradedAt) {
		t.Fatal("migration changed existing audit history")
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	e := audit.Event{OrganisationID: org, ActorID: id, ResourceType: "mission", ResourceID: id, Action: "scheduled", After: map[string]string{"name": "Mission A"}}
	if err := (audit.PostgresRecorder{}).Record(ctx, tx, e); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_type='mission' AND id>$1`, eventID).Scan(&count); err != nil || count != 1 {
		t.Fatal("generic event or sequence preservation failed", err)
	}
	// A recorder write is not committed independently of its caller's work.
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	e.Action = "cancelled"
	if err := (audit.PostgresRecorder{}).Record(ctx, tx, e); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='cancelled'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("recorder committed independently", err)
	}
	for _, sql := range []string{`UPDATE audit_events SET action='changed'`, `DELETE FROM audit_events`, `TRUNCATE audit_events`} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Fatal("upgrade removed append-only protection")
		}
	}
}

type failingRecorder struct{ cause error }

func (f failingRecorder) Record(context.Context, pgx.Tx, audit.Event) error { return f.cause }

func TestAuditRecorderFailureIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	var org, actor string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Recorder tests') RETURNING id::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	good := &drones.PostgresStore{Pool: pool, Recorder: audit.PostgresRecorder{}}
	d, err := good.Create(ctx, org, actor, drones.Metadata{Name: "Original", Vendor: "dji", Model: "Mavic", SerialNumber: "AUDIT-001"})
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("injected recorder failure")
	bad := &drones.PostgresStore{Pool: pool, Recorder: failingRecorder{cause: cause}}
	if _, err := bad.Create(ctx, org, actor, drones.Metadata{Name: "Rejected", Vendor: "dji", Model: "Mavic", SerialNumber: "AUDIT-002"}); !errors.Is(err, cause) {
		t.Fatal("create did not propagate recorder failure", err)
	}
	if _, err := bad.Update(ctx, org, actor, d.ID, drones.Patch{"name": "Rejected"}); !errors.Is(err, cause) {
		t.Fatal("update did not propagate recorder failure", err)
	}
	if err := bad.Delete(ctx, org, actor, d.ID); !errors.Is(err, cause) {
		t.Fatal("delete did not propagate recorder failure", err)
	}
	missing := &drones.PostgresStore{Pool: pool}
	if _, err := missing.Update(ctx, org, actor, d.ID, drones.Patch{"name": "Unaudited"}); err == nil {
		t.Fatal("missing recorder allowed a mutation")
	}
	actual, err := good.Get(ctx, org, d.ID)
	if err != nil || actual.Name != "Original" {
		t.Fatal("recorder failure did not roll back mutation", err)
	}
	items, _, err := good.List(ctx, org, drones.Filter{Limit: 20})
	if err != nil || len(items) != 1 {
		t.Fatal("failed create was committed", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events`).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed mutations wrote events", err)
	}
}
