package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/flightsummary"
)

func TestFlightSummaryIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	makeDrone := func(name string) (org, drone string) {
		t.Helper()
		err := pool.QueryRow(ctx, `WITH org AS (
			INSERT INTO organisations(name) VALUES($1) RETURNING id
		)
		INSERT INTO drones(organisation_id,name,vendor,model,serial_number)
		SELECT id,$1,'dji','Test model',$1 FROM org RETURNING organisation_id::text,id::text`, name).Scan(&org, &drone)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	org, drone := makeDrone("Local aircraft")
	otherOrg, otherDrone := makeDrone("Foreign aircraft")
	for _, sample := range []struct{ org, drone, id, timestamp, status string }{
		{org, drone, "prior", "2026-01-09 23:59:59Z", "in_mission"},
		{org, drone, "start", "2026-01-10 00:00:00Z", "in_mission"},
		{org, drone, "later", "2026-01-10 09:00:00Z", "available"},
		{org, drone, "next", "2026-01-11 00:00:00Z", "in_mission"},
		{otherOrg, otherDrone, "foreign", "2026-01-10 09:00:00Z", "in_mission"},
	} {
		_, err := pool.Exec(ctx, `WITH receipt AS (
			INSERT INTO telemetry_receipts(organisation_id,drone_id,source,event_id,observed_at,payload_hash)
			VALUES ($1,$2,'dji',$3,$4::text::timestamptz,decode(repeat('00',32),'hex'))
			RETURNING organisation_id,drone_id,source,event_id,observed_at
		)
		INSERT INTO telemetry_samples(organisation_id,drone_id,source,event_id,observed_at,status,flight_time_seconds,battery_cycles)
		SELECT organisation_id,drone_id,source,event_id,observed_at,$5,120,0 FROM receipt`,
			sample.org, sample.drone, sample.id, sample.timestamp, sample.status)
		if err != nil {
			t.Fatal(err)
		}
	}
	service := &flightsummary.Service{Reader: flightsummary.PostgresReader{Pool: pool}}
	token, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	request := func(role auth.Role, body string, want int) flightsummary.Report {
		t.Helper()
		mux := http.NewServeMux()
		registerFlightSummary(mux, &authHandler{config: AuthConfig{
			Origin: "http://localhost:5173", Store: testStore{user: auth.User{Role: role, OrganisationID: &org}},
		}}, service)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/flight-summary", strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d: %s", w.Code, want, w.Body)
		}
		var report flightsummary.Report
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
		}
		return report
	}
	request(auth.Pilot, `{"day":"2026-01-10"}`, 403)
	request(auth.FleetManager, `{"day":"2026-01-10","organisation_id":"`+otherOrg+`"}`, 400)
	facts := request(auth.FleetManager, `{"day":"2026-01-10"}`, 200)
	if facts.Status != "facts_only" || facts.Facts.Observations != 2 || facts.Facts.InMission != 1 ||
		facts.Facts.Aircraft != 1 || facts.Facts.AircraftInMission != 1 ||
		len(facts.Sources) != 1 || facts.Sources[0].DroneID != drone {
		t.Fatalf("incorrect tenant-scoped flight summary: %+v", facts)
	}
	noData := request(auth.FleetManager, `{"day":"2026-01-12","use_ai":true}`, 200)
	if noData.Status != "no_data" || noData.Sources == nil || len(noData.Sources) != 0 {
		t.Fatalf("unexpected empty report: %+v", noData)
	}
	fallback := request(auth.FleetManager, `{"day":"2026-01-10","use_ai":true}`, 200)
	if fallback.Status != "ai_not_configured" || fallback.Facts != facts.Facts || fallback.Narrative != facts.Narrative {
		t.Fatalf("expected fallback to computed facts: %+v", fallback)
	}
}
