package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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
			VALUES (NULLIF($1::text, '')::uuid, $4, $2, $3, $4) RETURNING id::text`, org, email, "test-hash", role)
		token, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = authStore.CreateSession(ctx, auth.Credentials{User: auth.User{ID: userID}, PasswordHash: "test-hash"}, token); err != nil {
			t.Fatal(err)
		}
		return userID, token
	}
	managerID, manager := session(org, "manager@example.test", auth.FleetManager)
	pilotID, pilot := session(org, "pilot@example.test", auth.Pilot)
	otherPilotID, otherPilot := session(org, "pilot2@example.test", auth.Pilot)
	_, foreign := session(otherOrg, "foreign@example.test", auth.FleetManager)
	_, admin := session("", "admin@example.test", auth.SystemAdministrator)
	droneID := id(`INSERT INTO drones (organisation_id, name, vendor, model, serial_number)
		VALUES ($1, 'Drone', 'dji', 'Demo', 'DEMO-1') RETURNING id::text`, org)
	store := &missions.PostgresStore{Pool: pool, Recorder: audit.PostgresRecorder{}}
	handler, err := NewAuthenticatedHandler(AuthConfig{Store: authStore, Missions: store, AuditHistory: audit.PostgresHistory{Pool: pool}, Origin: "http://localhost:5173"})
	if err != nil {
		t.Fatal(err)
	}
	apiRequest := func(method, path, token, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
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
	request := func(method, path, token, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		return apiRequest(method, "/missions"+path, token, body, want)
	}
	history := func(token, query string, want int) []audit.HistoryEntry {
		t.Helper()
		w := apiRequest("GET", "/audit-events?resource_type=mission"+query, token, "", want)
		var page struct{ Events []audit.HistoryEntry }
		if want == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Events == nil {
				t.Fatalf("invalid audit page: %s", w.Body)
			}
		}
		return page.Events
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
	if entries := history(manager, "", 200); len(entries) != 0 {
		t.Fatalf("rejected creates produced audit events: %+v", entries)
	}
	decode := func(w *httptest.ResponseRecorder) missions.Mission {
		t.Helper()
		var response struct{ Mission missions.Mission }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Mission.ID == "" {
			t.Fatalf("invalid mission response: %s", w.Body)
		}
		return response.Mission
	}
	created := decode(request("POST", "", manager, payload(pilotID, "Field"), 201))
	missionPath := "/" + created.ID
	request("POST", "", manager, payload(otherPilotID, "Field"), 409)
	request("PUT", missionPath, otherPilot, payload(otherPilotID, "Field"), 404)
	request("POST", missionPath+"/cancel", foreign, "", 404)
	store.Recorder = failingRecorder{cause: errors.New("injected mission audit failure")}
	request("PUT", missionPath, pilot, payload(pilotID, "Uncommitted location"), 503)
	request("POST", missionPath+"/cancel", pilot, "", 503)
	if entries := history(manager, "", 200); len(entries) != 1 || entries[0].Action != audit.Created {
		t.Fatalf("rejected changes produced audit events: %+v", entries)
	}
	store.Recorder = audit.PostgresRecorder{}
	edited := decode(request("PUT", missionPath, pilot, payload(pilotID, "Updated field"), 200))
	for token, count := range map[string]int{manager: 1, pilot: 1, otherPilot: 0, foreign: 0} {
		w := request("GET", "", token, "", 200)
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
	entries := history(manager, "", 200)
	if len(entries) != 3 {
		t.Fatalf("expected exactly three lifecycle events: %+v", entries)
	}
	for i, expected := range []struct {
		action audit.Action
		actor  string
	}{{"cancelled", pilotID}, {audit.Updated, pilotID}, {audit.Created, managerID}} {
		event := entries[i]
		if event.Action != expected.action || event.ActorID != expected.actor || event.OrganisationID != org ||
			event.ResourceID != created.ID || event.ResourceType != "mission" || event.CreatedAt.IsZero() {
			t.Fatalf("incorrect mission audit event: %+v", event)
		}
	}
	snapshot := func(raw json.RawMessage) missions.Mission {
		t.Helper()
		var mission missions.Mission
		if err := json.Unmarshal(raw, &mission); err != nil || mission.ID != created.ID {
			t.Fatalf("invalid mission audit snapshot: %s", raw)
		}
		return mission
	}
	cancelled := snapshot(entries[0].After)
	if cancelled.CancelledAt == nil {
		t.Fatal("cancelled snapshot is missing its cancellation timestamp")
	}
	cancelled.CancelledAt, cancelled.UpdatedAt = nil, edited.UpdatedAt
	if !reflect.DeepEqual(cancelled, edited) || !reflect.DeepEqual(snapshot(entries[0].Before), edited) ||
		!reflect.DeepEqual(snapshot(entries[1].After), edited) || !reflect.DeepEqual(snapshot(entries[1].Before), created) ||
		!reflect.DeepEqual(snapshot(entries[2].After), created) || string(entries[2].Before) != "null" {
		t.Fatal("mission audit snapshots do not match the committed lifecycle")
	}
	if got := history(admin, "&organisation_id="+org, 200); !reflect.DeepEqual(got, entries) {
		t.Fatalf("administrator did not see the same audit history: %+v", got)
	}
	if len(history(foreign, "", 200)) != 0 || len(history(admin, "&organisation_id="+otherOrg, 200)) != 0 {
		t.Fatal("mission audit history crossed organisation boundaries")
	}
	history(foreign, "&organisation_id="+org, 403)
	history(pilot, "", 403)
	store.Recorder = failingRecorder{cause: errors.New("injected mission audit failure")}
	request("POST", "", manager, payload(pilotID, "Audit failure"), 503)
	if got := id("SELECT count(*)::text FROM missions"); got != "1" {
		t.Fatalf("mission persisted despite audit failure: count=%s", got)
	}
	if got := history(manager, "", 200); !reflect.DeepEqual(got, entries) {
		t.Fatal("failed create changed the audit history")
	}
	store.Recorder = audit.PostgresRecorder{}
	request("POST", "", manager, payload(pilotID, "Rebook after cancellation"), 201)
}
