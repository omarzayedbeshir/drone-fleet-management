package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
)

func TestDroneRegistryIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var orgA, orgB string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Registry A') RETURNING id::text`).Scan(&orgA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Registry B') RETURNING id::text`).Scan(&orgB); err != nil {
		t.Fatal(err)
	}
	authStore := &auth.PostgresStore{Pool: pool}
	session := func(role auth.Role, org *string) (*http.Cookie, string) {
		t.Helper()
		token, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users(organisation_id,name,email,password_hash,role)
   VALUES($1,$2,$3,'fixture-hash',$2) RETURNING id::text`, org, role, strings.ToLower(token)+"@example.test").Scan(&id); err != nil {
			t.Fatal(err)
		}
		_, err = authStore.CreateSession(ctx, auth.Credentials{User: auth.User{ID: id}, PasswordHash: "fixture-hash"}, token)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: cookieName, Value: token}, id
	}
	manager, managerID := session(auth.FleetManager, &orgA)
	other, _ := session(auth.FleetManager, &orgB)
	pilot, _ := session(auth.Pilot, &orgA)
	technician, _ := session(auth.MaintenanceTechnician, &orgA)
	admin, _ := session(auth.SystemAdministrator, nil)
	registry := &drones.PostgresStore{Pool: pool, Recorder: audit.PostgresRecorder{}}
	handler, err := NewAuthenticatedHandler(AuthConfig{Store: authStore, Registry: registry, Origin: "http://localhost:5173"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/drones"+path, strings.NewReader(body))
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
	decode := func(w *httptest.ResponseRecorder) drones.Drone {
		t.Helper()
		var value struct {
			Drone drones.Drone `json:"drone"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value.Drone
	}
	type page struct {
		Drones     []drones.Drone `json:"drones"`
		Pagination struct {
			Limit  int  `json:"limit"`
			Offset int  `json:"offset"`
			More   bool `json:"has_more"`
		} `json:"pagination"`
	}
	list := func(path string, cookie *http.Cookie) page {
		t.Helper()
		w := request("GET", path, "", cookie)
		status(w, 200)
		var p page
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	payload := `{"name":" Survey 100%_\\ drone ","vendor":" DJI ","model":" Mavic ","serial_number":" Case-001 "}`
	status(request("GET", "", "", nil), 401)
	status(request("POST", "", payload, nil), 401)
	empty := list("", manager)
	if empty.Drones == nil || len(empty.Drones) != 0 || empty.Pagination.More {
		t.Fatal("empty list must contain an empty array")
	}
	created := request("POST", "", payload, manager)
	status(created, 201)
	drone := decode(created)
	if drone.OrganisationID != orgA || drone.Name != `Survey 100%_\ drone` || drone.Vendor != "dji" || drone.Model != "Mavic" || drone.SerialNumber != "Case-001" || drone.Status != "unknown" || drone.LastTelemetryAt != nil {
		t.Fatalf("bad creation: %+v", drone)
	}
	if created.Header().Get("Location") != "/api/v1/drones/"+drone.ID || !strings.HasSuffix(drone.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), "Z") {
		t.Fatal("missing resource location or UTC timestamp")
	}
	status(request("POST", "", payload, manager), 409)
	foreign := request("POST", "", payload, other)
	status(foreign, 201)
	foreignID := decode(foreign).ID
	for _, reader := range []*http.Cookie{manager, pilot, technician} {
		if len(list("", reader).Drones) != 1 {
			t.Fatal("list returned another tenant's drone")
		}
		status(request("GET", "/"+drone.ID, "", reader), 200)
	}
	status(request("GET", "", "", admin), 403)
	status(request("GET", "/"+drone.ID, "", admin), 403)
	for _, denied := range []*http.Cookie{pilot, technician, admin} {
		status(request("POST", "", payload, denied), 403)
		status(request("PATCH", "/"+drone.ID, `{"name":"Unauthorized"}`, denied), 403)
		status(request("DELETE", "/"+drone.ID, "", denied), 403)
	}
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		status(request(method, "/"+foreignID, `{"name":"Intruder"}`, manager), 404)
	}
	for _, body := range []string{`{}`, `null`, `{"name":null}`, `{"name":""}`, `{"status":"available"}`, `{"organisation_id":"` + orgB + `"}`, `{"last_telemetry_at":"2026-01-01"}`, `{"name":"x","vendor":"csv","model":"m","serial_number":"s"}`, `{"name":"x"} {}`} {
		status(request("POST", "", body, manager), 400)
	}
	for _, body := range []string{`{}`, `null`, `{"name":null}`, `{"name":""}`, `{"name":"Valid","model":null}`, `{"status":"available"}`, `{"organisation_id":"` + orgB + `"}`, `{"id":"other"}`, `{"name":42}`, `{"serial_number":"\u0000"}`} {
		status(request("PATCH", "/"+drone.ID, body, manager), 400)
	}
	status(request("GET", "/not-a-uuid", "", manager), 400)
	status(request("GET", "/00000000-0000-0000-0000-000000000000", "", manager), 404)
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		path := "/api/v1/drones"
		if method != "POST" {
			path += "/" + drone.ID
		}
		r := httptest.NewRequest(method, path, strings.NewReader(payload))
		r.AddCookie(manager)
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		status(w, 403)
	}
	for _, meta := range []string{`{"name":"Scout","vendor":"ardupilot","model":"Quad","serial_number":"AP-001"}`, `{"name":"Other","vendor":"dji","model":"Mavic","serial_number":"Case-002"}`} {
		status(request("POST", "", meta, manager), 201)
	}
	first := list("?limit=1", manager)
	second := list("?limit=1&offset=1", manager)
	if len(first.Drones) != 1 || len(second.Drones) != 1 || !first.Pagination.More || first.Drones[0].ID == second.Drones[0].ID {
		t.Fatal("pagination failed")
	}
	if len(list("?offset=100", manager).Drones) != 0 {
		t.Fatal("offset beyond results failed")
	}
	if len(list("?vendor=ardupilot&status=unknown", manager).Drones) != 1 || len(list("?status=available", manager).Drones) != 0 {
		t.Fatal("filters failed")
	}
	for _, query := range []string{"%25", "_", "%5C", "survey"} {
		p := list("?q="+query, manager)
		if len(p.Drones) != 1 || p.Drones[0].ID != drone.ID {
			t.Fatalf("literal search failed for %s: %+v", query, p)
		}
	}
	for _, query := range []string{"?limit=101", "?vendor=csv", "?status=flying", "?organisation_id=" + orgB, "?limit=1&limit=2"} {
		status(request("GET", query, "", manager), 400)
	}
	// Registry edits must preserve operational safety state and telemetry freshness.
	exec(`UPDATE drones SET status='grounded',last_telemetry_at='2026-01-01T00:00:00Z' WHERE id=$1`, drone.ID)
	updated := request("PATCH", "/"+drone.ID, `{"name":" Renamed "}`, manager)
	status(updated, 200)
	after := decode(updated)
	if after.Name != "Renamed" || after.Status != "grounded" || after.LastTelemetryAt == nil || after.SerialNumber != drone.SerialNumber || !after.UpdatedAt.After(drone.UpdatedAt) {
		t.Fatal("metadata update changed unrelated fields or failed timestamp")
	}
	status(request("PATCH", "/"+drone.ID, `{"serial_number":"Case-002"}`, manager), 409)
	current := decode(request("GET", "/"+drone.ID, "", manager))
	if current.SerialNumber != "Case-001" {
		t.Fatal("conflicting edit was not rolled back")
	}
	// Audit failure must roll back the drone write as well.
	exec(`CREATE FUNCTION reject_audit_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test audit failure'; END $$`)
	exec(`CREATE TRIGGER reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_audit_insert()`)
	status(request("PATCH", "/"+drone.ID, `{"name":"Must roll back"}`, manager), 503)
	status(request("DELETE", "/"+drone.ID, "", manager), 503)
	status(request("POST", "", `{"name":"Must roll back","vendor":"dji","model":"m","serial_number":"FAIL"}`, manager), 503)
	current = decode(request("GET", "/"+drone.ID, "", manager))
	if current.Name != "Renamed" {
		t.Fatal("failed audit persisted a mutation")
	}
	if len(list("?q=Must", manager).Drones) != 0 {
		t.Fatal("failed audit persisted a registration")
	}
	exec(`DROP TRIGGER reject_audit ON audit_events`)
	status(request("DELETE", "/"+drone.ID, "", manager), 204)
	status(request("GET", "/"+drone.ID, "", manager), 404)
	status(request("DELETE", "/"+drone.ID, "", manager), 404)
	rows, err := pool.Query(ctx, `SELECT action,actor_id::text,before_data,after_data FROM audit_events WHERE resource_type='drone' AND resource_id=$1 ORDER BY id`, drone.ID)
	if err != nil {
		t.Fatal(err)
	}
	actions := []string{}
	for rows.Next() {
		var action, actor string
		var before, after []byte
		if err := rows.Scan(&action, &actor, &before, &after); err != nil {
			t.Fatal(err)
		}
		if actor != managerID {
			t.Fatal("wrong audit actor")
		}
		actions = append(actions, action)
		if action == "created" && (before != nil || after == nil) {
			t.Fatal("bad create audit")
		}
		if action == "updated" && (!strings.Contains(string(before), `"name": "Survey`) || !strings.Contains(string(after), `"name": "Renamed"`)) {
			t.Fatal("missing before/after snapshots")
		}
		if action == "deleted" && (before == nil || after != nil) {
			t.Fatal("bad delete audit")
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(actions) != "[created updated deleted]" {
		t.Fatalf("unexpected audit actions %v", actions)
	}
	for _, sql := range []string{`UPDATE audit_events SET action='deleted' WHERE resource_type='drone' AND resource_id=$1`, `DELETE FROM audit_events WHERE resource_type='drone' AND resource_id=$1`} {
		_, err := pool.Exec(ctx, sql, drone.ID)
		pgErr, ok := err.(*pgconn.PgError)
		if !ok || pgErr.Code != "42501" {
			t.Fatalf("audit was mutable: %v", err)
		}
	}
	// Concurrent disjoint patches must both survive the row-locked update.
	concurrent, err := registry.Create(ctx, orgA, managerID, drones.Metadata{
		Name: "Concurrent", Vendor: "dji", Model: "Original", SerialNumber: "CONCURRENT",
	})
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan error, 2)
	for _, patch := range []drones.Patch{{"name": "Concurrent renamed"}, {"model": "New model"}} {
		go func(patch drones.Patch) {
			_, err := registry.Update(ctx, orgA, managerID, concurrent.ID, patch)
			updates <- err
		}(patch)
	}
	for i := 0; i < 2; i++ {
		if err := <-updates; err != nil {
			t.Fatal(err)
		}
	}
	concurrent, err = registry.Get(ctx, orgA, concurrent.ID)
	if err != nil || concurrent.Name != "Concurrent renamed" || concurrent.Model != "New model" {
		t.Fatalf("concurrent patch lost metadata: %+v %v", concurrent, err)
	}
	exec(`DELETE FROM organisations WHERE id=$1`, orgA)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organisation_id=$1`, orgA).Scan(&count); err != nil || count != 0 {
		t.Fatalf("tenant removal did not purge audit: %d %v", count, err)
	}
	status(request("GET", "/"+foreignID, "", other), 200)
}
