package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/fleetqa"
)

type qaModelFunc func(context.Context, string, string, int, json.RawMessage) (string, error)

func (f qaModelFunc) CompleteJSON(ctx context.Context, system, input string, limit int, schema json.RawMessage) (string, error) {
	return f(ctx, system, input, limit, schema)
}

func TestFleetQAIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	makeDrone := func(name string) string {
		t.Helper()
		var org string
		err := pool.QueryRow(ctx, `WITH org AS (
			INSERT INTO organisations(name) VALUES($1) RETURNING id
		)
		INSERT INTO drones(organisation_id,name,vendor,model,serial_number)
		SELECT id,$1,'dji','Test model',$1 FROM org RETURNING organisation_id::text`, name).Scan(&org)
		if err != nil {
			t.Fatal(err)
		}
		return org
	}
	org := makeDrone("Local aircraft")
	other := makeDrone("Foreign aircraft")
	for _, fixture := range []struct{ org, location, email string }{
		{org, "Private local location", "local@example.test"},
		{other, "Private foreign location", "foreign@example.test"},
	} {
		_, err := pool.Exec(ctx, `WITH pilot AS (
			INSERT INTO users (organisation_id, name, email, password_hash, role)
			VALUES ($1, 'Fixture pilot', $3, 'test-hash', 'pilot') RETURNING id
		)
		INSERT INTO missions (organisation_id, drone_id, pilot_id, location, starts_at, ends_at)
		SELECT d.organisation_id, d.id, p.id, $2, now() + interval '1 day', now() + interval '1 day 1 hour'
		FROM drones d CROSS JOIN pilot p WHERE d.organisation_id = $1`, fixture.org, fixture.location, fixture.email)
		if err != nil {
			t.Fatal(err)
		}
	}
	observed := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	for _, sample := range []struct {
		org, id, status string
		at              time.Time
	}{
		{org, "local-activity", "in_mission", observed},
		{org, "local-later", "charging", observed.Add(time.Minute)},
		{other, "foreign-activity", "in_mission", observed.Add(2 * time.Minute)},
	} {
		_, err := pool.Exec(ctx, `WITH receipt AS (
			INSERT INTO telemetry_receipts (organisation_id, drone_id, source, event_id, observed_at, payload_hash)
			SELECT organisation_id, id, 'dji', $2, $4, decode(repeat('00',32),'hex')
			FROM drones WHERE organisation_id = $1
			RETURNING organisation_id, drone_id, source, event_id, observed_at
		)
		INSERT INTO telemetry_samples (organisation_id, drone_id, source, event_id, observed_at, status, flight_time_seconds, battery_cycles)
		SELECT organisation_id, drone_id, source, event_id, observed_at, $3, 120, 0 FROM receipt`, sample.org, sample.id, sample.status, sample.at)
		if err != nil {
			t.Fatal(err)
		}
	}
	store := fleetqa.PostgresStore{Pool: pool}
	calls := 0
	service := &fleetqa.Service{Store: store, Model: qaModelFunc(func(_ context.Context, _, input string, _ int, schema json.RawMessage) (string, error) {
		calls++
		if !json.Valid(schema) {
			t.Fatal("expected a valid structured output schema")
		}
		if strings.Contains(input, "Foreign aircraft") || !strings.Contains(input, "Local aircraft") || strings.Contains(input, org) ||
			strings.Contains(input, "Private local location") || !strings.Contains(input, `"type":"mission"`) ||
			!strings.Contains(input, `"type":"telemetry"`) || !strings.Contains(input, `"next_observed_status":"charging"`) {
			t.Fatalf("unexpected provider data: %s", input)
		}
		switch calls {
		case 1:
			return `{"answer":"The local aircraft was observed in mission and next reported charging [3].","source_numbers":[3]}`, nil
		case 2:
			return `{"answer":"Invalid citation [99]","source_numbers":[1]}`, nil
		default:
			return "", errors.New("provider unavailable")
		}
	})}
	token, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	request := func(role auth.Role, body string, status int, contains string) {
		t.Helper()
		mux := http.NewServeMux()
		registerFleetQA(mux, &authHandler{config: AuthConfig{
			Origin: "http://localhost:5173", Store: testStore{user: auth.User{Role: role, OrganisationID: &org}},
		}}, service)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/fleet-qa", strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status || !strings.Contains(w.Body.String(), contains) || strings.Contains(w.Body.String(), "Foreign aircraft") {
			t.Fatalf("status=%d want=%d; body=%s", w.Code, status, w.Body)
		}
	}
	question := `{"question":"When was the last flight? What happened?"}`
	request(auth.Pilot, question, 403, "error")
	request(auth.FleetManager, `{"question":"Status?","organisation_id":"`+other+`"}`, 400, "error")
	request(auth.FleetManager, question, 200, `"number":3`)
	request(auth.FleetManager, question, 200, `"status":"unavailable"`)
	request(auth.FleetManager, question, 200, `"status":"unavailable"`)
	day := time.Now().UTC().Truncate(24 * time.Hour)
	var attempts sync.WaitGroup
	results := make(chan bool, fleetqa.DailyLimit)
	for range fleetqa.DailyLimit {
		attempts.Go(func() {
			allowed, err := store.Reserve(ctx, org, day)
			if err != nil {
				t.Error(err)
			}
			results <- allowed
		})
	}
	attempts.Wait()
	close(results)
	granted := 0
	for allowed := range results {
		if allowed {
			granted++
		}
	}
	if granted != fleetqa.DailyLimit-3 {
		t.Fatalf("quota granted %d further attempts, want %d", granted, fleetqa.DailyLimit-3)
	}
	request(auth.FleetManager, question, 200, `"status":"limit_reached"`)
	if calls != 3 {
		t.Fatalf("provider called after quota exhausted: %d", calls)
	}
	if allowed, err := store.Reserve(ctx, other, day); err != nil || !allowed {
		t.Fatalf("second organisation should have independent quota: %v, %v", allowed, err)
	}
	if allowed, err := store.Reserve(ctx, org, day.AddDate(0, 0, 1)); err != nil || !allowed {
		t.Fatalf("quota should reset on the next UTC day: %v, %v", allowed, err)
	}
}
