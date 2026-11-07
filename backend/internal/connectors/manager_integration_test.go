package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/catalog"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL")
	}
	ctx := context.Background()
	base, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(base.Close)
	schema := fmt.Sprintf("connector_test_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		if err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	files, _ := filepath.Glob("../../migrations/[0-9]*.sql")
	for _, f := range files {
		if filepath.Base(f) == "000001_enable_timescaledb.sql" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	return pool
}
func bindingFixture(t *testing.T, pool *pgxpool.Pool) Binding {
	t.Helper()
	ctx := context.Background()
	var b Binding
	b.Profile = "demo"
	b.Vendor = "dji"
	b.Serial = "D1"
	b.Active = true
	for _, v := range []struct {
		query  string
		args   []any
		target *string
	}{
		{`INSERT INTO organisations(name) VALUES('Connector tenant') RETURNING id::text`, nil, &b.Org},
	} {
		if err := pool.QueryRow(ctx, v.query, v.args...).Scan(v.target); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,'Manager',$2,'fixture-hash','fleet_manager') RETURNING id::text`, b.Org, newOwner()+"@example.test").Scan(&b.Actor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number) VALUES($1,'Drone','dji','Test','D1') RETURNING id::text`, b.Org).Scan(&b.Drone); err != nil {
		t.Fatal(err)
	}
	return b
}
func await(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("connector condition timed out")
}
func launch(m *Manager) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	return cancel, done
}
func TestConnectorCSVLifecycleIntegration(t *testing.T) {
	pool := testPool(t)
	b := bindingFixture(t, pool)
	ctx := context.Background()
	csv := filepath.Join(t.TempDir(), "sample.csv")
	raw := fmt.Sprintf("device_sn,timestamp,latitude,longitude,height,total_flight_time,battery_loop_times\nD1,%s,30.85,31.05,50,1,1\n", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339))
	if err := os.WriteFile(csv, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store := &PostgresStore{Pool: pool, Profiles: Profiles{"demo": {ID: "demo", Label: "Demo", Source: "csv", Vendor: "dji", Simulated: true, CSVPath: csv}}}
	view, err := store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", true})
	if err != nil || view.Connector.State != "pending" {
		t.Fatalf("save %+v %v", view, err)
	}
	cancel, done := launch(&Manager{Store: store, Poll: 10 * time.Millisecond})
	await(t, func() bool {
		v, e := store.Get(ctx, b.Org, b.Drone)
		return e == nil && v.Connector.State == "completed"
	})
	cancel()
	<-done
	// Completion survives process restart; no uncontrolled replay loop.
	cancel, done = launch(&Manager{Store: store, Poll: 10 * time.Millisecond})
	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_samples`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("samples %d err %v", count, err)
	}
	_, err = store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", false})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := store.Get(ctx, b.Org, b.Drone)
	if v.Connector.State != "disabled" {
		t.Fatal(v)
	}
}

type fakeAdapter struct {
	starts, active, max *atomic.Int32
	fail                bool
}

func (a *fakeAdapter) Health() adapters.Health { return adapters.Health{Connected: true, Ready: true} }
func (a *fakeAdapter) Run(ctx context.Context, _ adapters.Emit) error {
	a.starts.Add(1)
	active := a.active.Add(1)
	defer a.active.Add(-1)
	for {
		old := a.max.Load()
		if active <= old || a.max.CompareAndSwap(old, active) {
			break
		}
	}
	if a.fail {
		return errors.New("temporary startup error")
	}
	<-ctx.Done()
	return nil
}
func TestConnectorLeadershipDisableAndRevokeIntegration(t *testing.T) {
	pool := testPool(t)
	b := bindingFixture(t, pool)
	ctx := context.Background()
	store := &PostgresStore{Pool: pool, Profiles: Profiles{"demo": {ID: "demo", Label: "Demo", Source: "csv", Vendor: "dji", Simulated: true, CSVPath: "unused"}}}
	if _, err := store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", true}); err != nil {
		t.Fatal(err)
	}
	var starts, active, max atomic.Int32
	factory := func(Binding, Profile) (adapters.VendorAdapter, error) {
		return &fakeAdapter{starts: &starts, active: &active, max: &max}, nil
	}
	m := &Manager{Store: store, Factory: factory, Poll: 10 * time.Millisecond}
	cancel1, done1 := launch(m)
	cancel2, done2 := launch(m)
	defer func() { cancel1(); cancel2(); <-done1; <-done2 }()
	await(t, func() bool { return starts.Load() == 1 })
	time.Sleep(40 * time.Millisecond)
	if active.Load() != 1 || max.Load() != 1 {
		t.Fatal("competing supervisors launched duplicate workers")
	}
	if _, err := store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", false}); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return active.Load() == 0 })
	if _, err := store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", true}); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return starts.Load() == 2 })
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, b.Actor); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool {
		v, e := store.Get(ctx, b.Org, b.Drone)
		return e == nil && v.Connector.State == "failed" && active.Load() == 0
	})
}
func TestConnectorFencingAndAuditIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	b := bindingFixture(t, pool)
	store := &PostgresStore{Pool: pool, Profiles: Profiles{"demo": {ID: "demo", Label: "Demo", Source: "csv", Vendor: "dji", Simulated: true, CSVPath: "unused"}}}
	view, err := store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", true})
	if err != nil {
		t.Fatal(err)
	}
	b.Generation = view.Connector.Generation
	owner := newOwner()
	if ok, e := store.claim(ctx, b, owner); e != nil || !ok {
		t.Fatal(e)
	}
	sample := telemetry.Sample{EventID: "one", Source: "csv", ObservedAt: time.Now().UTC(), Status: "unknown", FlightTimeSeconds: 1, BatteryCycles: 1, FaultFlags: []string{}}
	if _, err = store.persist(ctx, b, owner, []telemetry.Sample{sample}); err != nil {
		t.Fatal(err)
	}
	// Revocation is fenced at ingestion, before any supervisor poll.
	if _, err = pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, b.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err = store.persist(ctx, b, owner, []telemetry.Sample{sample}); !errors.Is(err, ErrInactive) {
		t.Fatal("revoked manager was not fenced", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET is_active=true WHERE id=$1`, b.Actor); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", false}); err != nil {
		t.Fatal(err)
	}
	sample.EventID = "two"
	sample.ObservedAt = sample.ObservedAt.Add(time.Second)
	if _, err = store.persist(ctx, b, owner, []telemetry.Sample{sample}); !errors.Is(err, ErrInactive) {
		t.Fatalf("old worker not fenced: %v", err)
	}
	var count int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_samples`).Scan(&count)
	if count != 1 {
		t.Fatal("disabled worker wrote telemetry")
	}
	if _, err = pool.Exec(ctx, `CREATE FUNCTION fail_connector_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit unavailable'; END $$; CREATE TRIGGER fail_connector_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_connector_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", true}); err == nil {
		t.Fatal("save succeeded without audit")
	}
	v, _ := store.Get(ctx, b.Org, b.Drone)
	if v.Connector.Enabled {
		t.Fatal("failed audit persisted settings")
	}
	// A removed operator profile must not prevent disabling an existing connector.
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_connector_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	store.Profiles = Profiles{}
	if _, err = store.Save(ctx, b.Org, b.Actor, b.Drone, Settings{"demo", false}); err != nil {
		t.Fatal(err)
	}
	bjson, _ := json.Marshal(v)
	if len(bjson) == 0 {
		t.Fatal("empty view")
	}
}

func TestConnectorLeaderTakeoverIntegration(t *testing.T) {
	pool := testPool(t)
	b := bindingFixture(t, pool)
	store := &PostgresStore{Pool: pool, Profiles: Profiles{"demo": {ID: "demo", Label: "Demo", Source: "csv", Vendor: "dji", Simulated: true, CSVPath: "unused"}}}
	if _, err := store.Save(context.Background(), b.Org, b.Actor, b.Drone, Settings{"demo", true}); err != nil {
		t.Fatal(err)
	}
	var starts, active, max atomic.Int32
	m := &Manager{Store: store, Poll: 10 * time.Millisecond, Factory: func(Binding, Profile) (adapters.VendorAdapter, error) {
		return &fakeAdapter{starts: &starts, active: &active, max: &max}, nil
	}}
	cancel1, done1 := launch(m)
	await(t, func() bool { return starts.Load() == 1 })
	cancel2, done2 := launch(m)
	defer func() { cancel2(); <-done2 }()
	cancel1()
	<-done1
	await(t, func() bool { return starts.Load() == 2 })
	if max.Load() != 1 {
		t.Fatal("takeover overlapped workers")
	}
}

func TestConnectorRetriesAndReconfigurationIntegration(t *testing.T) {
	pool := testPool(t)
	b := bindingFixture(t, pool)
	store := &PostgresStore{Pool: pool, Profiles: Profiles{"demo": {ID: "demo", Label: "Demo", Source: "csv", Vendor: "dji", Simulated: true, CSVPath: "unused"}}}
	if _, err := store.Save(context.Background(), b.Org, b.Actor, b.Drone, Settings{"demo", true}); err != nil {
		t.Fatal(err)
	}
	var starts, active, max, builds atomic.Int32
	m := &Manager{Store: store, Poll: 10 * time.Millisecond, Factory: func(Binding, Profile) (adapters.VendorAdapter, error) {
		return &fakeAdapter{starts: &starts, active: &active, max: &max, fail: builds.Add(1) == 1}, nil
	}}
	cancel, done := launch(m)
	defer func() { cancel(); <-done }()
	await(t, func() bool { return starts.Load() == 2 && active.Load() == 1 })
	if _, err := store.Save(context.Background(), b.Org, b.Actor, b.Drone, Settings{"demo", true}); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return starts.Load() == 3 && active.Load() == 1 })
	if max.Load() != 1 {
		t.Fatal("reconfiguration overlapped workers")
	}
}

func TestVendorExtensionProcess(t *testing.T) {
	if os.Args[len(os.Args)-1] != "_vendor_extension_process" {
		return
	}
	var b struct {
		Protocol, Vendor, Serial string
		Flight                   int64 `json:"flight_time_seconds"`
		Cycles                   int64 `json:"battery_cycles"`
	}
	if json.NewDecoder(os.Stdin).Decode(&b) != nil || b.Protocol != "aerosync.telemetry.v1" {
		os.Exit(7)
	}
	_ = json.NewEncoder(os.Stdout).Encode(telemetry.Sample{EventID: "extension-observation", Source: b.Vendor, ObservedAt: time.Now().UTC().Add(-time.Minute), Status: "unknown", FlightTimeSeconds: b.Flight + 1, BatteryCycles: b.Cycles, FaultFlags: []string{}})
	os.Exit(0)
}
func TestNewVendorModuleIntegration(t *testing.T) {
	started := time.Now()
	ctx := context.Background()
	pool := testPool(t)
	b := bindingFixture(t, pool)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Installation is module + profile metadata only. No vendor-specific changes
	// to registry, common schema, writer, HTTP routes or frontend are involved.
	profiles := Profiles{"extension": {ID: "extension", Label: "Extension acceptance source", Source: "external", Vendor: "acceptance_vendor", VendorLabel: "Acceptance Vendor", OrganisationID: b.Org, Executable: exe, Arguments: []string{"-test.run=^TestVendorExtensionProcess$", "--", "_vendor_extension_process"}}}
	for _, p := range profiles {
		if err = p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	vendors, err := profiles.VendorDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	if err = catalog.Install(ctx, pool, vendors); err != nil {
		t.Fatal(err)
	}
	registry := &drones.PostgresStore{Pool: pool, Recorder: audit.PostgresRecorder{}}
	d, err := registry.Create(ctx, b.Org, b.Actor, drones.Metadata{Name: "New vendor", Vendor: "acceptance_vendor", Model: "Module test", SerialNumber: "MODULE-001"})
	if err != nil {
		t.Fatal(err)
	}
	store := &PostgresStore{Pool: pool, Profiles: profiles}
	if _, err = store.Save(ctx, b.Org, b.Actor, d.ID, Settings{"extension", true}); err != nil {
		t.Fatal(err)
	}
	cancel, done := launch(&Manager{Store: store, Poll: 10 * time.Millisecond})
	defer func() { cancel(); <-done }()
	await(t, func() bool { v, e := store.Get(ctx, b.Org, d.ID); return e == nil && v.Connector.State == "completed" })
	history, _, err := (&telemetry.PostgresStore{Pool: pool}).List(ctx, b.Org, d.ID, 20, 0)
	if err != nil || len(history) != 1 || history[0].Source != "acceptance_vendor" {
		t.Fatalf("new source history %+v %v", history, err)
	}
	entries, err := registry.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range entries {
		if v.Code == "acceptance_vendor" && v.Label == "Acceptance Vendor" {
			found = true
		}
	}
	if !found {
		t.Fatal("new vendor missing from UI catalog")
	}
	if _, err = registry.Create(ctx, b.Org, b.Actor, drones.Metadata{Name: "Uninstalled", Vendor: "uninstalled_vendor", Model: "Test", SerialNumber: "M2"}); !errors.Is(err, drones.ErrUnsupported) {
		t.Fatalf("uninstalled vendor accepted: %v", err)
	}
	t.Logf("new module -> catalog -> registry -> managed connector -> common writer completed in %s", time.Since(started))
}
