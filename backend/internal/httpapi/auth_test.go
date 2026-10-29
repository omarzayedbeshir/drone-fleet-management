package httpapi

import (
	"context"
	"errors"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testStore struct {
	user auth.User
	err  error
}

func (s testStore) FindCredentials(context.Context, string) (auth.Credentials, error) {
	return auth.Credentials{}, s.err
}
func (s testStore) CreateSession(context.Context, auth.Credentials, string) (auth.User, error) {
	return s.user, s.err
}
func (s testStore) ResolveSession(context.Context, string) (auth.User, error)    { return s.user, s.err }
func (s testStore) DeleteSession(context.Context, string) error                  { return s.err }
func (s testStore) ChangePassword(context.Context, string, string, string) error { return s.err }
func TestRoleAuthorization(t *testing.T) {
	tenant := "organisation-a"
	token, _ := auth.NewToken()
	for _, role := range []auth.Role{auth.SystemAdministrator, auth.FleetManager, auth.Pilot, auth.MaintenanceTechnician} {
		t.Run(string(role), func(t *testing.T) {
			user := auth.User{ID: "user", OrganisationID: &tenant, Role: role}
			if role == auth.SystemAdministrator {
				user.OrganisationID = nil
			}
			a := &authHandler{config: AuthConfig{Store: testStore{user: user}}}
			r := httptest.NewRequest("GET", "/", nil)
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
			w := httptest.NewRecorder()
			a.authenticated(func(w http.ResponseWriter, r *http.Request) {
				if currentUser(r).OrganisationID == nil || *currentUser(r).OrganisationID != tenant {
					t.Fatal("lost tenant context")
				}
				w.WriteHeader(200)
			}, auth.FleetManager)(w, r)
			want := 403
			if role == auth.FleetManager {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("status=%d want=%d", w.Code, want)
			}
		})
	}
}
func TestAuthenticationFailureModes(t *testing.T) {
	token, _ := auth.NewToken()
	for _, tt := range []struct {
		name   string
		store  testStore
		status int
	}{
		{"expired", testStore{err: auth.ErrUnauthenticated}, 401},
		{"database unavailable", testStore{err: errors.New("database unavailable")}, 503},
		{"unknown role", testStore{user: auth.User{Role: "owner"}}, 403},
		{"missing tenant", testStore{user: auth.User{Role: auth.Pilot}}, 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &authHandler{config: AuthConfig{Store: tt.store}}
			r := httptest.NewRequest("GET", "/", nil)
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
			w := httptest.NewRecorder()
			a.authenticated(a.me)(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}
func TestOriginProtection(t *testing.T) {
	a := &authHandler{config: AuthConfig{Origin: "https://aerosync.example"}}
	for _, origin := range []string{"", "null", "https://evil.example", "https://aerosync.example.evil.test"} {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		a.sameOrigin(func(http.ResponseWriter, *http.Request) { t.Fatal("untrusted request reached handler") })(w, r)
		if w.Code != 403 {
			t.Fatalf("origin=%q status=%d", origin, w.Code)
		}
	}
}
func TestLimiter(t *testing.T) {
	l := &loginLimiter{windows: make(map[string]loginWindow)}
	for i := 0; i < 10; i++ {
		if !l.allow("127.0.0.1:1000") {
			t.Fatal("limited too early")
		}
	}
	if l.allow("127.0.0.1:2000") {
		t.Fatal("port bypassed limiter")
	}
	if !l.allow("127.0.0.2:1000") {
		t.Fatal("unrelated client throttled")
	}
	l.windows["127.0.0.1"] = loginWindow{started: time.Now().Add(-time.Minute), attempts: 10}
	if !l.allow("127.0.0.1:1000") {
		t.Fatal("window did not reset")
	}
}
func TestStrictRequestBodies(t *testing.T) {
	for _, body := range []string{`{"email":"a@example.test","role":"system_administrator"}`, `{} {}`, strings.Repeat("x", 5000)} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var input struct {
			Email string `json:"email"`
		}
		w := httptest.NewRecorder()
		if readJSON(w, r, &input) {
			t.Fatal("accepted invalid body")
		}
	}
}
func TestCookieSecurity(t *testing.T) {
	a := &authHandler{config: AuthConfig{SecureCookies: true}}
	c := a.cookie("token")
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Domain != "" || c.Path != "/api" {
		t.Fatal("insecure session cookie")
	}
}

func TestRejectsUnsafeConfiguration(t *testing.T) {
	for _, config := range []AuthConfig{
		{Store: testStore{}, Origin: "http://localhost:5173", SecureCookies: true},
		{Store: testStore{}, Origin: "https://aerosync.example/", SecureCookies: true},
		{Store: testStore{}, Origin: "https://aerosync.example", SecureCookies: false},
		{Store: testStore{}, Origin: "null", SecureCookies: true},
		{Origin: "https://aerosync.example", SecureCookies: true},
	} {
		if _, err := NewAuthenticatedHandler(config); err == nil {
			t.Fatalf("accepted unsafe config: %+v", config)
		}
	}
}
