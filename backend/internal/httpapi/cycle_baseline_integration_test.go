package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func TestCycleBaselineAPIIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	var org, other, id, foreign string
	for _, out := range []*string{&org, &other} {
		if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Cycle tenant') RETURNING id::text`).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	for i, out := range []*string{&id, &foreign} {
		tenant := org
		if i == 1 {
			tenant = other
		}
		if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number,status) VALUES($1,'Drone','ardupilot','Test','C1','grounded') RETURNING id::text`, tenant).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	authStore := &auth.PostgresStore{Pool: pool}
	session := func(role auth.Role, tenant *string) (*http.Cookie, string) {
		token, _ := auth.NewToken()
		var actor string
		if err := pool.QueryRow(ctx, `INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,'Test',$2,'fixture-hash',$3) RETURNING id::text`, tenant, strings.ToLower(token)+"@example.test", role).Scan(&actor); err != nil {
			t.Fatal(err)
		}
		if _, err := authStore.CreateSession(ctx, auth.Credentials{User: auth.User{ID: actor}, PasswordHash: "fixture-hash"}, token); err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: cookieName, Value: token}, actor
	}
	manager, managerID := session(auth.FleetManager, &org)
	tech, techID := session(auth.MaintenanceTechnician, &org)
	pilot, _ := session(auth.Pilot, &org)
	admin, _ := session(auth.SystemAdministrator, nil)
	store := &telemetry.PostgresStore{Pool: pool}
	handler, err := NewAuthenticatedHandler(AuthConfig{Store: authStore, Telemetry: store, Origin: "http://localhost:5173"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, drone, body string, cookie *http.Cookie, origin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/drones/"+drone+"/battery-cycles", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin {
			r.Header.Set("Origin", "http://localhost:5173")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
		}
	}
	payload := func(total, expected, revision int64) string {
		return fmt.Sprintf(`{"total":%d,"expected_total":%d,"expected_revision":%d,"reason":"Service log B-207"}`, total, expected, revision)
	}
	check(request("GET", id, "", nil, true), 401)
	check(request("PUT", id, payload(207, 0, 0), pilot, true), 403)
	check(request("GET", id, "", admin, true), 403)
	check(request("PUT", id, payload(207, 0, 0), admin, true), 403)
	check(request("GET", foreign, "", manager, true), 404)
	check(request("PUT", foreign, payload(207, 0, 0), tech, true), 404)
	check(request("PUT", id, payload(207, 0, 0), manager, false), 403)
	for _, body := range []string{`{}`, `null`, `{"total":null}`, `{"total":1.5}`, `{"total":1,"expected_total":0,"expected_revision":0,"reason":"tiny"}`, `{"total":1,"expected_total":0,"expected_revision":0,"reason":"Valid log","source":"telemetry"}`} {
		check(request("PUT", id, body, manager, true), 400)
	}
	before, err := store.ReadCycleBaseline(ctx, org, id)
	if err != nil || before.Source != "unknown" || before.Total != 0 {
		t.Fatalf("initial %+v %v", before, err)
	}
	check(request("PUT", id, payload(207, 0, 0), tech, true), 200)
	manual, err := store.ReadCycleBaseline(ctx, org, id)
	if err != nil || manual.Source != "manual" || manual.Total != 207 || manual.Revision != 1 || manual.RecordedAt == nil || manual.Note != "Service log B-207" {
		t.Fatalf("manual %+v %v", manual, err)
	}
	var state string
	var last *time.Time
	if err = pool.QueryRow(ctx, `SELECT status,last_telemetry_at FROM drones WHERE id=$1`, id).Scan(&state, &last); err != nil || state != "grounded" || last != nil {
		t.Fatal("manual reading changed freshness or grounding", err)
	}
	for _, cookie := range []*http.Cookie{manager, tech, pilot} {
		check(request("GET", id, "", cookie, true), 200)
	}
	check(request("PUT", id, payload(208, 0, 0), manager, true), 409)
	check(request("PUT", id, payload(206, 207, 1), manager, true), 409)
	var count int
	var actor string
	if err = pool.QueryRow(ctx, `SELECT count(*),max(actor_id::text) FROM audit_events WHERE resource_type='battery_cycles' AND resource_id=$1`, id).Scan(&count, &actor); err != nil || count != 1 || actor != techID {
		t.Fatalf("audit %d %s %v", count, actor, err)
	}
	if _, err = pool.Exec(ctx, `CREATE FUNCTION fail_cycle_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'unavailable'; END $$; CREATE TRIGGER fail_cycle_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_cycle_audit()`); err != nil {
		t.Fatal(err)
	}
	check(request("PUT", id, payload(208, 207, 1), manager, true), 503)
	got, _ := store.ReadCycleBaseline(ctx, org, id)
	if got.Total != 207 || got.Revision != 1 {
		t.Fatal("audit failure changed baseline")
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM battery_cycle_readings`).Scan(&count)
	if count != 1 {
		t.Fatal("audit failure persisted ledger row")
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER fail_cycle_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	// Without any prior telemetry, even the first observation must respect the
	// documented baseline. Later data cannot erase its historical boundary.
	raw := func(event string, at time.Time, cycles int64, source string) []byte {
		b, _ := json.Marshal(telemetry.Sample{EventID: event, Source: source, ObservedAt: at, Status: "available", FlightTimeSeconds: 0, BatteryCycles: cycles, FaultFlags: []string{}})
		return b
	}
	if _, err = store.Ingest(ctx, org, managerID, id, raw("bad-first", manual.RecordedAt.Add(time.Second), 206, "ardupilot")); err == nil {
		t.Fatal("first telemetry bypassed baseline")
	}
	if _, err = store.Ingest(ctx, org, managerID, id, raw("fresh", manual.RecordedAt.Add(time.Second), 207, "ardupilot")); err != nil {
		t.Fatal(err)
	}
	got, _ = store.ReadCycleBaseline(ctx, org, id)
	if got.Source != "manual" || got.Note != manual.Note {
		t.Fatal("ArduPilot echo overwrote manual provenance")
	}
	if _, err = store.Ingest(ctx, org, managerID, id, raw("bad-before", manual.RecordedAt.Add(-time.Second), 208, "ardupilot")); err == nil {
		t.Fatal("past observation contradicted manual reading")
	}
	if _, err = pool.Exec(ctx, `UPDATE battery_cycle_readings SET reason='changed'`); err == nil {
		t.Fatal("manual reading ledger was mutable")
	}
	if _, err = store.RecordCycleBaseline(ctx, org, techID, id, telemetry.CycleReading{Total: 207, ExpectedTotal: 207, ExpectedRevision: 1, Reason: "Verified again from log"}); err != nil {
		t.Fatal(err)
	}
	// Role checks remain enforced in the repository if a session/worker races a revocation.
	if _, err = pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, techID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RecordCycleBaseline(ctx, org, techID, id, telemetry.CycleReading{Total: 207, ExpectedTotal: 207, ExpectedRevision: 2, Reason: "Another log reference"}); !errors.Is(err, telemetry.ErrCyclePermission) {
		t.Fatal("inactive actor accepted", err)
	}
}
func TestCycleProvenanceIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	var org, id, actor string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Provenance') RETURNING id::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,'Manager','cycle-source@example.test','fixture-hash','fleet_manager') RETURNING id::text`, org).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number) VALUES($1,'Drone','dji','Test','P1') RETURNING id::text`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	store := &telemetry.PostgresStore{Pool: pool}
	at := time.Now().UTC().Add(-time.Minute)
	ingest := func(event, source string, when time.Time, cycles int64) {
		t.Helper()
		raw, _ := json.Marshal(telemetry.Sample{EventID: event, Source: source, ObservedAt: when, Status: "unknown", FlightTimeSeconds: 0, BatteryCycles: cycles, FaultFlags: []string{}})
		if _, err := store.Ingest(ctx, org, actor, id, raw); err != nil {
			t.Fatal(err)
		}
	}
	ingest("sim", "csv", at, 20)
	got, _ := store.ReadCycleBaseline(ctx, org, id)
	if got.Source != "simulated" {
		t.Fatal(got)
	}
	ingest("vendor", "dji", at.Add(time.Second), 21)
	got, _ = store.ReadCycleBaseline(ctx, org, id)
	if got.Source != "telemetry" {
		t.Fatal(got)
	}
	manual, err := store.RecordCycleBaseline(ctx, org, actor, id, telemetry.CycleReading{Total: 22, ExpectedTotal: 21, ExpectedRevision: 0, Reason: "Verified log reference"})
	if err != nil {
		t.Fatal(err)
	}
	ingest("after-manual", "dji", manual.RecordedAt.Add(time.Second), 23)
	got, _ = store.ReadCycleBaseline(ctx, org, id)
	if got.Source != "telemetry" || got.Note != "" {
		t.Fatal(got)
	}
	raw, _ := json.Marshal(telemetry.Sample{EventID: "late-contradiction", Source: "dji", ObservedAt: manual.RecordedAt.Add(-time.Millisecond), Status: "unknown", FlightTimeSeconds: 0, BatteryCycles: 23, FaultFlags: []string{}})
	if _, err = store.Ingest(ctx, org, actor, id, raw); err == nil {
		t.Fatal("newer telemetry erased the manual historical boundary")
	}
}
