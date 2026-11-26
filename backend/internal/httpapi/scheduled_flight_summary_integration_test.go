package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/flightsummary"
)

type summaryNarrator func(context.Context, flightsummary.Facts) (string, error)

func (f summaryNarrator) Narrate(ctx context.Context, facts flightsummary.Facts) (string, error) {
	return f(ctx, facts)
}

func TestScheduledFlightSummaryIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	store := &flightsummary.PostgresSchedule{Pool: pool}
	var organisations [2]string
	for i := range organisations {
		if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES ('Summary schedule test') RETURNING id::text`).Scan(&organisations[i]); err != nil {
			t.Fatal(err)
		}
	}
	org, other := organisations[0], organisations[1]
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	for i, id := range organisations {
		var drone string
		if err := pool.QueryRow(ctx, `INSERT INTO drones(organisation_id,name,vendor,model,serial_number)
			VALUES ($1,'Summary aircraft','dji','Test model','SUMMARY-1') RETURNING id::text`, id).Scan(&drone); err != nil {
			t.Fatal(err)
		}
		for sample := 0; sample <= i; sample++ {
			_, err := pool.Exec(ctx, `WITH receipt AS (
				INSERT INTO telemetry_receipts(organisation_id,drone_id,source,event_id,observed_at,payload_hash)
				VALUES ($1,$2,'dji',$3,$4,decode(repeat('00',32),'hex'))
				RETURNING organisation_id,drone_id,source,event_id,observed_at
			)
			INSERT INTO telemetry_samples(organisation_id,drone_id,source,event_id,observed_at,status,flight_time_seconds,battery_cycles)
			SELECT organisation_id,drone_id,source,event_id,observed_at,'in_mission',120,0 FROM receipt`,
				id, drone, []string{"sample-one", "sample-two"}[sample], day.Add(time.Duration(sample)*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	token, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	const actor = "6ad836c3-7959-4ad2-b046-02db58d47ad8"
	request := func(user auth.User, method, body string, want int) flightsummary.Schedule {
		t.Helper()
		mux := http.NewServeMux()
		registerScheduledFlightSummary(mux, &authHandler{config: AuthConfig{
			Origin: "http://localhost:5173", Store: testStore{user: user},
		}}, store)
		r := httptest.NewRequest(method, "/api/v1/flight-summary/schedule", strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s got %d, want %d: %s", method, w.Code, want, w.Body)
		}
		var result flightsummary.Schedule
		if want == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	manager := auth.User{ID: actor, Role: auth.FleetManager, OrganisationID: &org}
	foreign := auth.User{ID: actor, Role: auth.FleetManager, OrganisationID: &other}
	pilot := auth.User{ID: actor, Role: auth.Pilot, OrganisationID: &org}
	if got := request(manager, "GET", "", 200); got.Enabled || got.Latest != nil {
		t.Fatalf("delivery should be disabled initially: %+v", got)
	}
	request(pilot, "PUT", `{"enabled":true}`, 403)
	request(manager, "PUT", `{"enabled":true,"organisation_id":"`+other+`"}`, 400)
	request(manager, "PUT", `{"enabled":true}`, 204)
	request(manager, "PUT", `{"enabled":true}`, 204)

	var calls atomic.Int32
	service := flightsummary.Service{Writer: summaryNarrator(func(_ context.Context, facts flightsummary.Facts) (string, error) {
		calls.Add(1)
		if facts.Observations != 1 || facts.Aircraft != 1 || facts.InMission != 1 {
			t.Errorf("provider received incorrect organisation totals: %+v", facts)
		}
		return "One observed in-mission aircraft.", nil
	})}
	now := day.AddDate(0, 0, 1).Add(time.Minute)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			if err := store.DeliverDue(ctx, service, now); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	got := request(manager, "GET", "", 200)
	if calls.Load() != 1 || !got.Enabled || got.Latest == nil || got.Latest.DeliveredAt.IsZero() ||
		got.Latest.Report.Day != day.Format(time.DateOnly) || got.Latest.Report.Status != "ai_generated" {
		t.Fatalf("unexpected scheduled delivery: %+v; provider calls=%d", got, calls.Load())
	}
	if result := request(foreign, "GET", "", 200); result.Enabled || result.Latest != nil {
		t.Fatalf("delivery crossed organisations: %+v", result)
	}
	request(manager, "PUT", `{"enabled":false}`, 204)
	if err := store.DeliverDue(ctx, service, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if disabled := request(manager, "GET", "", 200); disabled.Enabled || !reflect.DeepEqual(disabled.Latest, got.Latest) {
		t.Fatalf("disabling must retain old delivery and stop new ones: %+v", disabled)
	}
	var auditChanges string
	if err := pool.QueryRow(ctx, `SELECT string_agg((before_data->>'enabled') || '->' || (after_data->>'enabled'), ',' ORDER BY id)
		FROM audit_events WHERE organisation_id=$1 AND actor_id=$2 AND resource_type='flight_summary_settings'`, org, actor).Scan(&auditChanges); err != nil || auditChanges != "false->true,true->false" {
		t.Fatalf("unexpected settings audit history: %q, %v", auditChanges, err)
	}
}
