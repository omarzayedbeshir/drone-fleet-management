package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

// Requires a disposable database. Each run creates and drops an isolated schema.
func TestAuthenticationIntegration(t *testing.T) {
	ctx := context.Background()
	pool := newIntegrationPool(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var orgA, orgB string
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('A') RETURNING id::text`).Scan(&orgA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organisations(name) VALUES('B') RETURNING id::text`).Scan(&orgB); err != nil {
		t.Fatal(err)
	}
	password := "original-password-123"
	// Cost-policy hashing is covered in auth_test; keep fixture comparisons fast
	// under the race detector while still exercising real bcrypt verification.
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	hash := string(hashBytes)
	if err != nil {
		t.Fatal(err)
	}
	roles := []auth.Role{auth.SystemAdministrator, auth.FleetManager, auth.Pilot, auth.MaintenanceTechnician}
	for _, role := range roles {
		var tenant any = orgA
		if role == auth.SystemAdministrator {
			tenant = nil
		}
		exec(`INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,$2,$3,$4,$5)`, tenant, string(role), string(role)+"@example.test", hash, role)
	}
	exec(`INSERT INTO users(organisation_id,name,email,password_hash,role) VALUES($1,'Other tenant','other@example.test',$2,'pilot')`, orgB, hash)
	handler, err := NewAuthenticatedHandler(AuthConfig{Store: &auth.PostgresStore{Pool: pool}, Origin: "http://localhost:5173"})
	if err != nil {
		t.Fatal(err)
	}
	counter := 0
	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		counter++
		r := httptest.NewRequest(method, "/api/v1/auth/"+path, strings.NewReader(body))
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", counter)
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
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
	}
	login := func(email, password string, previous *http.Cookie) *http.Cookie {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"email": email, "password": password})
		w := request("POST", "login", string(body), previous)
		status(w, 200)
		if strings.Contains(w.Body.String(), "password") {
			t.Fatal("password data leaked")
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
			t.Fatal("invalid cookie")
		}
		return cookies[0]
	}
	status(request("GET", "me", "", nil), 401)
	for _, role := range roles {
		cookie := login(" "+strings.ToUpper(string(role))+"@EXAMPLE.TEST ", password, nil)
		w := request("GET", "me?organisation_id="+orgB+"&role=system_administrator", "", cookie)
		status(w, 200)
		var data struct {
			User auth.User `json:"user"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if data.User.Role != role {
			t.Fatal("wrong role or client role accepted")
		}
		if role == auth.SystemAdministrator {
			if data.User.OrganisationID != nil {
				t.Fatal("admin has tenant")
			}
		} else if data.User.OrganisationID == nil || *data.User.OrganisationID != orgA {
			t.Fatal("tenant scope overwritten")
		}
		status(request("POST", "logout", `{}`, cookie), 204)
		status(request("GET", "me", "", cookie), 401)
		status(request("POST", "logout", `{}`, cookie), 204)
	}
	other := login("other@example.test", password, nil)
	w := request("GET", "me", "", other)
	status(w, 200)
	if !strings.Contains(w.Body.String(), orgB) || strings.Contains(w.Body.String(), orgA) {
		t.Fatal("cross-tenant identity leak")
	}
	wrong := request("POST", "login", `{"email":"pilot@example.test","password":"wrong"}`, nil)
	unknown := request("POST", "login", `{"email":"missing@example.test","password":"wrong"}`, nil)
	status(wrong, 401)
	status(unknown, 401)
	if wrong.Body.String() != unknown.Body.String() {
		t.Fatal("account existence leaked")
	}
	status(request("POST", "login", `{"email":"pilot@example.test","password":"original-password-123","role":"system_administrator"}`, nil), 400)
	cookie := login("pilot@example.test", password, nil)
	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM auth_sessions WHERE token_hash=$1`, auth.TokenHash(cookie.Value)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == cookie.Value || len(stored) != 32 {
		t.Fatal("raw session token stored")
	}
	rotated := login("pilot@example.test", password, cookie)
	if rotated.Value == cookie.Value {
		t.Fatal("session fixation")
	}
	status(request("GET", "me", "", cookie), 401)
	exec(`UPDATE auth_sessions SET last_seen_at=now()-interval '31 minutes' WHERE token_hash=$1`, auth.TokenHash(rotated.Value))
	status(request("GET", "me", "", rotated), 401)
	cookie = login("pilot@example.test", password, nil)
	exec(`UPDATE auth_sessions SET created_at=now()-interval '25 hours',expires_at=now()-interval '1 hour' WHERE token_hash=$1`, auth.TokenHash(cookie.Value))
	status(request("GET", "me", "", cookie), 401)
	cookie = login("pilot@example.test", password, nil)
	exec(`UPDATE users SET is_active=false WHERE email='pilot@example.test'`)
	status(request("GET", "me", "", cookie), 401)
	inactive := request("POST", "login", `{"email":"pilot@example.test","password":"original-password-123"}`, nil)
	status(inactive, 401)
	if inactive.Body.String() != wrong.Body.String() {
		t.Fatal("inactive state leaked")
	}
	exec(`UPDATE users SET is_active=true WHERE email='pilot@example.test'`)
	status(request("GET", "me", "", cookie), 401)
	cookie = login("pilot@example.test", password, nil)
	exec(`UPDATE users SET role='maintenance_technician' WHERE email='pilot@example.test'`)
	status(request("GET", "me", "", cookie), 401)
	cookie = login("pilot@example.test", password, nil)
	exec(`UPDATE users SET organisation_id=$1 WHERE email='pilot@example.test'`, orgB)
	status(request("GET", "me", "", cookie), 401)
	cookie = login("pilot@example.test", password, nil)
	second := login("pilot@example.test", password, nil)
	status(request("POST", "password", `{"current_password":"wrong","new_password":"replacement-password-456"}`, cookie), 400)
	status(request("GET", "me", "", cookie), 200)
	status(request("POST", "password", `{"current_password":"original-password-123","new_password":"short"}`, cookie), 400)
	status(request("POST", "password", `{"current_password":"original-password-123","new_password":"replacement-password-456"}`, cookie), 204)
	status(request("GET", "me", "", cookie), 401)
	status(request("GET", "me", "", second), 401)
	status(request("POST", "login", `{"email":"pilot@example.test","password":"original-password-123"}`, nil), 401)
	cookie = login("pilot@example.test", "replacement-password-456", nil)
	exec(`DELETE FROM organisations WHERE id=$1`, orgB)
	status(request("GET", "me", "", cookie), 401)
	status(request("GET", "me", "", other), 401)
}
