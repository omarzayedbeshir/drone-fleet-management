package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
)

func TestAuditHistoryIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t, "000005_shared_audit_events.sql")
	makeOrg := func(name string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, "INSERT INTO organisations(name) VALUES($1) RETURNING id::text", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	org, other := makeOrg("Audit fleet"), makeOrg("Other fleet")
	actor := "6ad836c3-7959-4ad2-b046-02db58d47ad8"
	for _, event := range []struct{ org, resource, name string }{
		{org, "drone", "Original drone"}, {other, "mission", "Other tenant mission"}, {org, "mission", "Local mission"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO audit_events
			(organisation_id,actor_id,resource_type,resource_id,action,after_data)
			VALUES ($1,$2,$3,$2,'created',jsonb_build_object('name',$4::text))`,
			event.org, actor, event.resource, event.name); err != nil {
			t.Fatal(err)
		}
	}
	token, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	type page struct {
		Events []audit.HistoryEntry
		Next   *string `json:"next_before_id"`
	}
	request := func(user auth.User, query string, want int) page {
		t.Helper()
		mux := http.NewServeMux()
		registerAuditHistory(mux, &authHandler{config: AuthConfig{Store: testStore{user: user}}}, audit.PostgresHistory{Pool: pool})
		r := httptest.NewRequest("GET", "/api/v1/audit-events"+query, nil)
		if user.Role != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: status %d, want %d: %s", query, w.Code, want, w.Body)
		}
		var result page
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	manager := auth.User{ID: actor, Role: auth.FleetManager, OrganisationID: &org}
	first := request(manager, "?limit=1", 200)
	if len(first.Events) != 1 || first.Next == nil || first.Events[0].ResourceType != "mission" ||
		first.Events[0].OrganisationID != org || string(first.Events[0].Before) != "null" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	next := request(manager, "?limit=1&before_id="+*first.Next, 200)
	if len(next.Events) != 1 || next.Next != nil || next.Events[0].ResourceType != "drone" ||
		next.Events[0].OrganisationID != org || next.Events[0].ID >= first.Events[0].ID {
		t.Fatalf("unexpected second page: %+v", next)
	}
	filtered := request(manager, "?resource_type=mission", 200)
	if len(filtered.Events) != 1 || filtered.Events[0].ID != first.Events[0].ID {
		t.Fatalf("unexpected filtered page: %+v", filtered)
	}
	request(auth.User{}, "", 401)
	request(auth.User{Role: auth.Pilot, OrganisationID: &org}, "", 403)
	request(manager, "?organisation_id="+other, 403)
	request(manager, "?before_id=0", 400)
	admin := auth.User{ID: actor, Role: auth.SystemAdministrator}
	request(admin, "", 400)
	adminPage := request(admin, "?organisation_id="+other, 200)
	if len(adminPage.Events) != 1 || adminPage.Events[0].OrganisationID != other {
		t.Fatalf("unexpected administrator page: %+v", adminPage)
	}
}
