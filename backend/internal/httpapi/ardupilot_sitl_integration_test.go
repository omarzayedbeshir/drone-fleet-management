package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/ardupilot"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func TestArduPilotSITLIntegration(t *testing.T) {
	endpoint := os.Getenv("SITL_MAVLINK_ENDPOINT")
	if endpoint == "" {
		t.Skip("set SITL_MAVLINK_ENDPOINT for real SITL-to-database verification")
	}
	pool := newIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var org, actor, id string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('SITL test tenant') RETURNING id::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,'SITL operator','sitl@example.test','fixture-hash','fleet_manager') RETURNING id::text`, org).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number) VALUES($1,'Real SITL','ardupilot','ArduCopter','SITL-001') RETURNING id::text`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	store := &telemetry.PostgresStore{Pool: pool}
	adapter, err := ardupilot.New(ardupilot.Config{Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	gpsSamples := 0
	accepted := 0
	var replay []byte
	if err := adapter.Run(ctx, func(ctx context.Context, sample telemetry.Sample) error {
		raw, err := json.Marshal(sample)
		if err != nil {
			return err
		}
		result, err := store.Ingest(ctx, org, actor, id, raw)
		if err != nil {
			return err
		}
		if result.Duplicate {
			t.Error("live observation unexpectedly duplicated")
		}
		accepted++
		if sample.Latitude != nil {
			gpsSamples++
			if replay == nil {
				replay = raw
			}
		}
		if gpsSamples >= 3 {
			cancel()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if gpsSamples < 3 {
		t.Fatalf("missing GPS-backed observations; health=%+v", adapter.Health())
	}
	queryCtx := context.Background()
	var samples, receipts, rejections int
	for table, target := range map[string]*int{"telemetry_samples": &samples, "telemetry_receipts": &receipts, "telemetry_rejections": &rejections} {
		if err := pool.QueryRow(queryCtx, "SELECT count(*) FROM "+table).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if samples != accepted || receipts != accepted || rejections != 0 {
		t.Fatalf("inconsistent SITL persistence samples=%d receipts=%d rejected=%d accepted=%d", samples, receipts, rejections, accepted)
	}
	result, err := store.Ingest(queryCtx, org, actor, id, replay)
	if err != nil || !result.Duplicate {
		t.Fatal("real SITL replay was not idempotent", err)
	}
	var consistent bool
	if err := pool.QueryRow(queryCtx, `SELECT d.last_telemetry_at=s.observed_at AND d.flight_time_seconds=s.flight_time_seconds AND d.battery_cycles=s.battery_cycles
 FROM drones d JOIN LATERAL(SELECT * FROM telemetry_samples WHERE drone_id=d.id ORDER BY observed_at DESC LIMIT 1)s ON true WHERE d.id=$1`, id).Scan(&consistent); err != nil || !consistent {
		t.Fatal("latest projection does not match persisted SITL history", err)
	}
	t.Logf("SITL -> adapter -> TimescaleDB verified: %d observations, %d GPS-backed, no rejections, duplicate replay accepted", accepted, gpsSamples)
}
