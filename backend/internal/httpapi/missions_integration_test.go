package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/missions"
)

func TestMissionAPIIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	id := func(sql string, args ...any) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	org := id("INSERT INTO organisations (name) VALUES ('Fleet') RETURNING id::text")
	otherOrg := id("INSERT INTO organisations (name) VALUES ('Other fleet') RETURNING id::text")
	authStore := &auth.PostgresStore{Pool: pool}
	session := func(org, email string, role auth.Role) (string, string) {
		t.Helper()
		userID := id(`INSERT INTO users (organisation_id, name, email, password_hash, role)
			VALUES ($1, $4, $2, $3, $4) RETURNING id::text`, org, email, "test-hash", role)
		token, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = authStore.CreateSession(ctx, auth.Credentials{User: auth.User{ID: userID}, PasswordHash: "test-hash"}, token); err != nil {
			t.Fatal(err)
		}
		return userID, token
	}
	_, manager := session(org, "manager@example.test", auth.FleetManager)
	pilotID, pilot := session(org, "pilot@example.test", auth.Pilot)
	otherPilotID, otherPilot := session(org, "pilot2@example.test", auth.Pilot)
	_, foreign := session(otherOrg, "foreign@example.test", auth.FleetManager)
	droneID := id(`INSERT INTO drones (organisation_id, name, vendor, model, serial_number)
		VALUES ($1, 'Drone', 'dji', 'Demo', 'DEMO-1') RETURNING id::text`, org)
	store := &missions.PostgresStore{Pool: pool, Recorder: audit.PostgresRecorder{}}
	handler, err := NewAuthenticatedHandler(AuthConfig{Store: authStore, Missions: store, Origin: "http://localhost:5173"})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, token, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/missions"+path, strings.NewReader(body))
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	payload := func(pilotID, location string) string {
		return fmt.Sprintf(`{"drone_id":%q,"pilot_id":%q,"location":%q,"starts_at":"2026-11-27T09:00:00Z","ends_at":"2026-11-27T10:00:00Z"}`, droneID, pilotID, location)
	}

	for _, tc := range []struct {
		token          string
		drones, pilots int
	}{{manager, 1, 2}, {pilot, 1, 1}, {foreign, 0, 0}} {
		w := request("GET", "/assignment-options", tc.token, "", 200)
		var options missions.AssignmentOptions
		if err := json.Unmarshal(w.Body.Bytes(), &options); err != nil ||
			options.Drones == nil || options.Pilots == nil ||
			len(options.Drones) != tc.drones || len(options.Pilots) != tc.pilots {
			t.Fatalf("unexpected options: %s", w.Body)
		}
		if tc.token == pilot && options.Pilots[0].ID != pilotID {
			t.Fatalf("pilot received another pilot's option: %s", w.Body)
		}
	}
	request("GET", "", "", "", 401)
	request("POST", "", pilot, payload(otherPilotID, "Field"), 403)
	request("POST", "", foreign, payload(pilotID, "Field"), 400)
	w := request("POST", "", manager, payload(pilotID, "Field"), 201)
	var response struct {
		Mission missions.Mission `json:"mission"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Mission.ID == "" {
		t.Fatalf("invalid create response: %s", w.Body)
	}
	missionPath := "/" + response.Mission.ID
	request("POST", "", manager, payload(otherPilotID, "Field"), 409)
	request("PUT", missionPath, otherPilot, payload(otherPilotID, "Field"), 404)
	request("PUT", missionPath, pilot, payload(pilotID, "Updated field"), 200)
	for token, count := range map[string]int{manager: 1, pilot: 1, otherPilot: 0, foreign: 0} {
		w = request("GET", "", token, "", 200)
		var page struct{ Missions []missions.ListItem }
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Missions) != count {
			t.Fatalf("expected %d visible missions: %s", count, w.Body)
		}
		if count == 1 && page.Missions[0].DroneStatus != "unknown" {
			t.Fatalf("expected unknown without fresh telemetry: %s", w.Body)
		}
	}
	request("POST", missionPath+"/cancel", pilot, "", 204)
	request("POST", missionPath+"/cancel", pilot, "", 204)
	request("PUT", missionPath, pilot, payload(pilotID, "Field"), 409)
	if got := id(`SELECT string_agg(action, ',' ORDER BY id) FROM audit_events
		WHERE resource_type='mission' AND resource_id=$1`, response.Mission.ID); got != "created,updated,cancelled" {
		t.Fatalf("unexpected mission audit history: %s", got)
	}
	store.Recorder = nil
	request("POST", "", manager, payload(pilotID, "Audit failure"), 503)
	if got := id("SELECT count(*)::text FROM missions"); got != "1" {
		t.Fatalf("mission persisted despite audit failure: count=%s", got)
	}
	store.Recorder = audit.PostgresRecorder{}
	request("POST", "", manager, payload(pilotID, "Rebook after cancellation"), 201)
}
