package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/csvreplay"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/ingestion"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func TestRecordedPipelineIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	var org, id, actor, other string
	for _, v := range []struct {
		sql string
		out *string
	}{
		{`INSERT INTO organisations(name) VALUES('Pipeline') RETURNING id::text`, &org},
		{`INSERT INTO organisations(name) VALUES('Other') RETURNING id::text`, &other},
	} {
		if err := pool.QueryRow(ctx, v.sql).Scan(v.out); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number) VALUES($1,'Recorded DJI','dji','Simulated','SIM-DJI-A001') RETURNING id::text`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,'Manager','pipeline@example.test','fixture-hash','fleet_manager') RETURNING id::text`, org).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	store := &telemetry.PostgresStore{Pool: pool, Now: func() time.Time { return time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC) }}
	source, err := csvreplay.New(csvreplay.Config{Path: filepath.Join("..", "..", "testdata", "dji_sample.csv"), Vendor: "dji"})
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		a, err := ingestion.New(ingestion.Config{Source: source, Serial: "SIM-DJI-A001", Vendor: "dji", Capacity: 2})
		if err != nil {
			t.Fatal(err)
		}
		if err = a.Run(ctx, func(ctx context.Context, s telemetry.Sample) error {
			b, _ := json.Marshal(s)
			_, err := store.Ingest(ctx, org, actor, id, b)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if a.Health().Rejected != 0 {
			t.Fatalf("pipeline rejected fixtures: %+v", a.Health())
		}
	}
	run()
	run() // full replay is idempotent
	history, more, err := store.List(ctx, org, id, 100, 0)
	if err != nil || more || len(history) != 40 {
		t.Fatalf("history %d more %v err %v", len(history), more, err)
	}
	if history[0].Source != "csv" || history[0].BatteryCycles != 207 || history[0].FlightTimeSeconds <= 248400 {
		t.Fatalf("bad stored counters/source %+v", history[0])
	}
	if _, _, err = store.List(ctx, other, id, 100, 0); err != telemetry.ErrNotFound {
		t.Fatalf("cross-tenant read: %v", err)
	}
	var raw string
	if err = pool.QueryRow(ctx, `SELECT row_to_json(s)::text FROM telemetry_samples s LIMIT 1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	t.Logf("Recorded DJI pipeline: %d samples, replay deduplicated, latest UTC=%s", len(history), history[0].ObservedAt)
}
func TestTelemetryBatchIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	var org, id, actor string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Batch') RETURNING id::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number) VALUES($1,'Batch','dji','Test','B1') RETURNING id::text`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	store := &telemetry.PostgresStore{Pool: pool}
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	raw := func(event string, delta time.Duration, counter int64) []byte {
		b, _ := json.Marshal(telemetry.Sample{EventID: event, Source: "dji", ObservedAt: base.Add(delta), Status: "available", FlightTimeSeconds: counter, BatteryCycles: counter, FaultFlags: []string{}})
		return b
	}
	one := raw("one", 0, 1)
	two := raw("two", time.Second, 2)
	results, err := store.IngestBatch(ctx, org, actor, id, [][]byte{one, one, []byte("{"), two, raw("regress", 2*time.Second, 1), raw("one", 0, 3)})
	if err != nil {
		t.Fatal(err)
	}
	if !results[1].Result.Duplicate || results[2].Rejection == nil || results[4].Rejection == nil || results[5].Rejection == nil {
		t.Fatalf("bad outcomes %+v", results)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_samples`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("count %d err %v", count, err)
	}
	rated := int64(500)
	if _, err = store.ConfigureBattery(ctx, org, actor, id, telemetry.BatteryRating{RatedCycles: &rated, AlertFraction: 0.8}); err != nil {
		t.Fatal(err)
	}
	// Failed audit insert rolls back battery configuration.
	if _, err = pool.Exec(ctx, `CREATE FUNCTION fail_battery_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER fail_battery_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_battery_audit()`); err != nil {
		t.Fatal(err)
	}
	rated = 600
	if _, err = store.ConfigureBattery(ctx, org, actor, id, telemetry.BatteryRating{RatedCycles: &rated, AlertFraction: 0.8}); err == nil {
		t.Fatal("accepted missing audit")
	}
	var got int64
	if err = pool.QueryRow(ctx, `SELECT battery_rated_cycles FROM drones WHERE id=$1`, id).Scan(&got); err != nil || got != 500 {
		t.Fatalf("rating %d err %v", got, err)
	}
}
func TestTelemetryWriterUpgradeIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t, "000006_telemetry_ingestion.sql")
	for _, name := range []string{"000007_telemetry_writer.sql", "000008_telemetry_receipt_timestamp.sql"} {
		sql, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(fmt.Errorf("upgrade %s: %w", name, err))
		}
	}
	var retention string
	if err := pool.QueryRow(ctx, `SELECT retention::text FROM telemetry_storage_settings`).Scan(&retention); err != nil {
		t.Fatal(err)
	}
}

func TestTelemetryFreshnessIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	var org, id string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Freshness') RETURNING id::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number,status,last_telemetry_at) VALUES($1,'Fresh','dji','Test','F1','available',$2) RETURNING id::text`, org, at).Scan(&id); err != nil {
		t.Fatal(err)
	}
	store := &drones.PostgresStore{Pool: pool, Freshness: 30 * time.Second, Now: func() time.Time { return at.Add(31 * time.Second) }}
	d, err := store.Get(ctx, org, id)
	if err != nil || d.Status != "unknown" || d.LastTelemetryAt == nil || !d.LastTelemetryAt.Equal(at) {
		t.Fatalf("stale %+v %v", d, err)
	}
	for _, status := range []string{"available", "unknown"} {
		items, _, err := store.List(ctx, org, drones.Filter{Status: status, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if status == "unknown" {
			want = 1
		}
		if len(items) != want {
			t.Fatalf("filter %s: %d", status, len(items))
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE drones SET status='grounded' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	d, err = store.Get(ctx, org, id)
	if err != nil || d.Status != "grounded" {
		t.Fatalf("lost grounding %+v %v", d, err)
	}
}

func TestTelemetryReceiptAfterRetentionIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	var org, id, actor string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Retention') RETURNING id::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number) VALUES($1,'Retained','dji','Test','R1') RETURNING id::text`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	store := &telemetry.PostgresStore{Pool: pool}
	at := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Second)
	s := telemetry.Sample{EventID: "retained", Source: "dji", ObservedAt: at, Status: "available", FlightTimeSeconds: 100, BatteryCycles: 10, FaultFlags: []string{}}
	raw, _ := json.Marshal(s)
	first, err := store.Ingest(ctx, org, actor, id, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `SELECT public.drop_chunks('telemetry_samples'::regclass,older_than=>$1::timestamptz)`, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_samples`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retention count %d err %v", count, err)
	}
	retry, err := store.Ingest(ctx, org, actor, id, raw)
	if err != nil || !retry.Duplicate || !retry.Sample.ReceivedAt.Equal(first.Sample.ReceivedAt) {
		t.Fatalf("retained retry %+v %v", retry, err)
	}
	s.EventID = "newer-regression"
	s.ObservedAt = at.Add(time.Hour)
	s.FlightTimeSeconds = 99
	raw, _ = json.Marshal(s)
	if _, err = store.Ingest(ctx, org, actor, id, raw); err == nil {
		t.Fatal("counter regression bypassed projection after retention")
	}
}
