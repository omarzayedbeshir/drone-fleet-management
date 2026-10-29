package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func TestTelemetryIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var org, otherOrg, id, otherID string
	for i, target := range []*string{&org, &otherOrg} {
		if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES($1) RETURNING id::text`, fmt.Sprintf("Telemetry tenant %d", i)).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	for i, target := range []*string{&id, &otherID} {
		tenant := org
		if i == 1 {
			tenant = otherOrg
		}
		if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number) VALUES($1,'Test','dji','Mavic','S1') RETURNING id::text`, tenant).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	authStore := &auth.PostgresStore{Pool: pool}
	session := func(role auth.Role, tenant *string) (*http.Cookie, string) {
		t.Helper()
		token, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		var actor string
		if err := pool.QueryRow(ctx, `INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,'Test',$2,'fixture-hash',$3) RETURNING id::text`, tenant, strings.ToLower(token)+"@example.test", role).Scan(&actor); err != nil {
			t.Fatal(err)
		}
		if _, err := authStore.CreateSession(ctx, auth.Credentials{User: auth.User{ID: actor}, PasswordHash: "fixture-hash"}, token); err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: cookieName, Value: token}, actor
	}
	manager, actor := session(auth.FleetManager, &org)
	pilot, _ := session(auth.Pilot, &org)
	tech, _ := session(auth.MaintenanceTechnician, &org)
	admin, _ := session(auth.SystemAdministrator, nil)
	now := time.Now().UTC().Truncate(time.Second)
	base := now.Add(-3 * time.Minute)
	store := &telemetry.PostgresStore{Pool: pool, Now: func() time.Time { return now }}
	handler, err := NewAuthenticatedHandler(AuthConfig{Store: authStore, Registry: &drones.PostgresStore{Pool: pool, Recorder: audit.PostgresRecorder{}}, Telemetry: store, Origin: "http://localhost:5173"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, drone, query, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/drones/"+drone+"/telemetry"+query, strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	status := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		if w.Code != want {
			t.Fatalf("status=%d want=%d: %s", w.Code, want, w.Body.String())
		}
	}
	body := func(event string, at time.Time, seconds, cycles int64, changes map[string]any) string {
		values := map[string]any{"event_id": event, "source": "dji", "observed_at": at.Format(time.RFC3339Nano), "status": "available", "flight_time_seconds": seconds, "battery_cycles": cycles, "fault_flags": []string{"GPS"}}
		for key, value := range changes {
			values[key] = value
		}
		raw, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	decode := func(w *httptest.ResponseRecorder) telemetry.Result {
		t.Helper()
		var value telemetry.Result
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := body("one", base, 100, 3, nil)
	status(request("POST", id, "", first, nil), 401)
	for _, cookie := range []*http.Cookie{pilot, tech, admin} {
		status(request("POST", id, "", first, cookie), 403)
	}
	status(request("POST", otherID, "", first, manager), 404)
	status(request("GET", otherID, "", "", manager), 404)
	if count("telemetry_rejections") != 0 {
		t.Fatal("unauthorized requests wrote tenant logs")
	}
	created := request("POST", id, "", first, manager)
	status(created, 201)
	accepted := decode(created)
	if accepted.Duplicate || !accepted.LatestUpdated {
		t.Fatal("initial sample was not applied")
	}
	duplicate := request("POST", id, "", " \n"+first+" \n", manager)
	status(duplicate, 200)
	retry := decode(duplicate)
	if !retry.Duplicate || retry.LatestUpdated || !retry.Sample.ReceivedAt.Equal(accepted.Sample.ReceivedAt) {
		t.Fatal("retry changed the accepted event")
	}
	status(request("POST", id, "", body("one", base, 101, 3, nil), manager), 409)
	status(request("POST", id, "", body("other-id", base, 100, 3, nil), manager), 409)
	for _, invalid := range []string{
		"{", body("bad-battery", base.Add(time.Second), 100, 3, map[string]any{"battery_percent": 101}),
		body("bad-source", base.Add(time.Second), 100, 3, map[string]any{"source": "ardupilot"}),
		body("bad-counter", base.Add(time.Second), -1, 3, nil),
		body("bad-private", base.Add(time.Second), 100, 3, map[string]any{"imagery": "SECRET-IMAGE"}),
		body("bad-coordinates", base.Add(time.Second), 100, 3, map[string]any{"latitude": 91, "longitude": 31}),
	} {
		w := request("POST", id, "", invalid, manager)
		status(w, 422)
		if strings.Contains(w.Body.String(), "SECRET-IMAGE") {
			t.Fatal("raw payload leaked")
		}
	}
	status(request("POST", id, "", strings.Repeat("x", telemetry.MaxBody+100), manager), 413)
	if count("telemetry_samples") != 1 || count("telemetry_receipts") != 1 || count("telemetry_rejections") != 9 {
		t.Fatal("invalid/duplicate input changed accepted storage")
	}
	var badActors int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM telemetry_rejections WHERE actor_id<>$1 OR organisation_id<>$2 OR octet_length(payload_hash)<>32`, actor, org).Scan(&badActors); err != nil || badActors != 0 {
		t.Fatal("incorrect rejection metadata", err)
	}
	latest := request("POST", id, "", body("three", base.Add(2*time.Minute), 300, 5, map[string]any{"status": "in_mission", "fault_flags": []string{"BATTERY"}}), manager)
	status(latest, 201)
	late := request("POST", id, "", body("two", base.Add(time.Minute), 200, 4, nil), manager)
	status(late, 201)
	if decode(late).LatestUpdated {
		t.Fatal("late sample overwrote newer projection")
	}
	status(request("POST", id, "", body("bad-late", base.Add(30*time.Second), 350, 5, nil), manager), 422)
	status(request("POST", id, "", body("bad-new", base.Add(3*time.Minute), 299, 5, nil), manager), 422)
	assertProjection := func(seconds, cycles int64, at time.Time, state string) {
		t.Helper()
		var gotSeconds, gotCycles int64
		var gotAt time.Time
		var gotStatus string
		if err := pool.QueryRow(ctx, `SELECT flight_time_seconds,battery_cycles,last_telemetry_at,status FROM drones WHERE id=$1`, id).Scan(&gotSeconds, &gotCycles, &gotAt, &gotStatus); err != nil {
			t.Fatal(err)
		}
		if gotSeconds != seconds || gotCycles != cycles || !gotAt.Equal(at) || gotStatus != state {
			t.Fatalf("inconsistent projection: %d %d %s %s", gotSeconds, gotCycles, gotAt, gotStatus)
		}
	}
	assertProjection(300, 5, base.Add(2*time.Minute), "in_mission")
	exec(`UPDATE drones SET status='grounded' WHERE id=$1`, id)
	grounded := request("POST", id, "", body("four", base.Add(3*time.Minute), 400, 6, nil), manager)
	status(grounded, 201)
	assertProjection(400, 6, base.Add(3*time.Minute), "grounded")
	for _, cookie := range []*http.Cookie{manager, pilot, tech} {
		status(request("GET", id, "?limit=2", "", cookie), 200)
	}
	status(request("GET", id, "", "", admin), 403)
	for _, query := range []string{"?limit=101", "?offset=-1", "?organisation_id=other", "?limit=1&limit=2"} {
		status(request("GET", id, query, "", manager), 400)
	}
	var listing struct {
		Samples    []telemetry.Stored `json:"samples"`
		Pagination struct {
			More bool `json:"has_more"`
		} `json:"pagination"`
	}
	w := request("GET", id, "?limit=2", "", manager)
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Samples) != 2 || !listing.Pagination.More || listing.Samples[0].EventID != "four" || listing.Samples[1].EventID != "three" {
		t.Fatal("incorrect history pagination/order")
	}
	var hypertables int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM timescaledb_information.hypertables WHERE hypertable_schema=current_schema() AND hypertable_name='telemetry_samples'`).Scan(&hypertables); err != nil || hypertables != 1 {
		t.Fatal("telemetry is not a hypertable", err)
	}
	// A projection failure must also roll back the sample and its dedup receipt.
	exec(`CREATE FUNCTION fail_projection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected projection failure'; END $$`)
	exec(`CREATE TRIGGER fail_projection BEFORE UPDATE ON drones FOR EACH ROW EXECUTE FUNCTION fail_projection()`)
	fifth := body("five", base.Add(4*time.Minute), 500, 7, nil)
	status(request("POST", id, "", fifth, manager), 503)
	if count("telemetry_samples") != 4 || count("telemetry_receipts") != 4 {
		t.Fatal("partial ingestion survived projection failure")
	}
	assertProjection(400, 6, base.Add(3*time.Minute), "grounded")
	exec(`DROP TRIGGER fail_projection ON drones`)
	status(request("POST", id, "", fifth, manager), 201)
	// Rejection logging also fails closed if its durable insert is unavailable.
	rejectedBefore := count("telemetry_rejections")
	exec(`CREATE FUNCTION fail_rejection() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected rejection failure'; END $$`)
	exec(`CREATE TRIGGER fail_rejection BEFORE INSERT ON telemetry_rejections FOR EACH ROW EXECUTE FUNCTION fail_rejection()`)
	status(request("POST", id, "", "{", manager), 503)
	if count("telemetry_rejections") != rejectedBefore {
		t.Fatal("failed rejection unexpectedly committed")
	}
	exec(`DROP TRIGGER fail_rejection ON telemetry_rejections`)
	// Concurrent retries are accepted once, even through separate transactions.
	sixth := []byte(body("six", base.Add(5*time.Minute), 600, 8, nil))
	type outcome struct {
		result telemetry.Result
		err    error
	}
	outcomes := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() { result, err := store.Ingest(ctx, org, actor, id, sixth); outcomes <- outcome{result, err} }()
	}
	duplicates := 0
	for i := 0; i < 2; i++ {
		out := <-outcomes
		if out.err != nil {
			t.Fatal(out.err)
		}
		if out.result.Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 || count("telemetry_samples") != 6 || count("telemetry_receipts") != 6 {
		t.Fatal("concurrent retry was not idempotent")
	}
	assertProjection(600, 8, base.Add(5*time.Minute), "grounded")
	status(request("POST", id, "", body("csv-one", base.Add(6*time.Minute), 700, 9, map[string]any{"source": "csv"}), manager), 201)
	// Operational history prevents registry removal; audit and deletion roll back.
	deleteRequest := httptest.NewRequest("DELETE", "/api/v1/drones/"+id, nil)
	deleteRequest.Header.Set("Origin", "http://localhost:5173")
	deleteRequest.AddCookie(manager)
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	status(deleted, 409)
	if count("audit_events") != 0 {
		t.Fatal("failed deletion wrote an audit event")
	}
	for _, table := range []string{"telemetry_samples", "telemetry_receipts", "telemetry_rejections"} {
		for _, statement := range []string{"DELETE FROM " + table, "UPDATE " + table + " SET organisation_id=organisation_id", "TRUNCATE " + table + " CASCADE"} {
			_, err := pool.Exec(ctx, statement)
			pgErr, ok := err.(*pgconn.PgError)
			if !ok || pgErr.Code != "42501" {
				t.Fatalf("%s was mutable: %v", table, err)
			}
		}
	}
	exec(`DELETE FROM organisations WHERE id=$1`, org)
	if count("telemetry_samples") != 0 || count("telemetry_receipts") != 0 || count("telemetry_rejections") != 0 {
		t.Fatal("organisation removal left telemetry records")
	}
}
