package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/connectors"
)

func TestConnectorAPIIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	var org, other, id, foreign string
	for _, out := range []*string{&org, &other} {
		if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('Connector API') RETURNING id::text`).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	for i, out := range []*string{&id, &foreign} {
		tenant := org
		if i == 1 {
			tenant = other
		}
		if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number) VALUES($1,'Test','dji','Test','D1') RETURNING id::text`, tenant).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	authStore := &auth.PostgresStore{Pool: pool}
	session := func(role auth.Role, tenant *string) *http.Cookie {
		token, _ := auth.NewToken()
		var actor string
		if err := pool.QueryRow(ctx, `INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,'Test',$2,'fixture-hash',$3) RETURNING id::text`, tenant, strings.ToLower(token)+"@example.test", role).Scan(&actor); err != nil {
			t.Fatal(err)
		}
		if _, err := authStore.CreateSession(ctx, auth.Credentials{User: auth.User{ID: actor}, PasswordHash: "fixture-hash"}, token); err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: cookieName, Value: token}
	}
	manager := session(auth.FleetManager, &org)
	pilot := session(auth.Pilot, &org)
	tech := session(auth.MaintenanceTechnician, &org)
	admin := session(auth.SystemAdministrator, nil)
	store := &connectors.PostgresStore{Pool: pool, Profiles: connectors.Profiles{
		"demo":    {ID: "demo", Label: "Demo", Source: "csv", Vendor: "dji", Simulated: true, CSVPath: "/private/sample.csv"},
		"foreign": {ID: "foreign", Label: "Private", Source: "dji", Vendor: "dji", OrganisationID: other, Endpoint: "https://private.example", CredentialRef: "SECRET_REF"},
	}}
	handler, err := NewAuthenticatedHandler(AuthConfig{Store: authStore, Connectors: store, Origin: "http://localhost:5173"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, drone, body string, cookie *http.Cookie, origin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/drones/"+drone+"/connector", strings.NewReader(body))
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
	payload := `{"profile_id":"demo","enabled":true}`
	check(request("GET", id, "", nil, true), 401)
	check(request("PUT", id, payload, nil, true), 401)
	for _, cookie := range []*http.Cookie{pilot, tech, admin} {
		check(request("PUT", id, payload, cookie, true), 403)
	}
	check(request("GET", id, "", admin, true), 403)
	check(request("GET", foreign, "", manager, true), 404)
	check(request("PUT", foreign, payload, manager, true), 404)
	check(request("PUT", id, payload, manager, false), 403)
	for _, body := range []string{`{}`, `null`, `{"profile_id":"demo","enabled":null}`, `{"profile_id":"demo","enabled":true,"endpoint":"http://private"}`, `{"profile_id":"foreign","enabled":true}`} {
		check(request("PUT", id, body, manager, true), 400)
	}
	check(request("GET", "not-a-uuid", "", manager, true), 400)
	w := request("PUT", id, payload, manager, true)
	check(w, 200)
	var view connectors.View
	if err = json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Connector.Generation != 1 || view.Connector.State != "pending" || len(view.Profiles) != 1 {
		t.Fatalf("unexpected view %+v", view)
	}
	for _, cookie := range []*http.Cookie{manager, pilot, tech} {
		w = request("GET", id, "", cookie, true)
		check(w, 200)
		for _, secret := range []string{"/private", "SECRET_REF", "private.example", "actor_id", "runtime_owner"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("leaked %s", secret)
			}
		}
	}
	check(request("PUT", id, `{"profile_id":"demo","enabled":false}`, manager, true), 200)
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE resource_type='connector' AND resource_id=$1`, id).Scan(&count); err != nil || count != 2 {
		t.Fatalf("audit count %d err %v", count, err)
	}
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_connector_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'unavailable'; END $$; CREATE TRIGGER reject_connector_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_connector_audit()`); err != nil {
		t.Fatal(err)
	}
	check(request("PUT", id, payload, manager, true), 503)
	view, err = store.Get(ctx, org, id)
	if err != nil || view.Connector.Enabled {
		t.Fatal("failed audit persisted enable", err)
	}
}
