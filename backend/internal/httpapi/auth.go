package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
	"golang.org/x/crypto/bcrypt"
)

const cookieName = "aerosync_session"

type AuthConfig struct {
	Store         auth.Store
	Registry      drones.Store
	Telemetry     telemetry.Store
	Origin        string
	SecureCookies bool
}

type authHandler struct {
	config        AuthConfig
	dummyHash     []byte
	limiter       *loginLimiter
	passwordSlots chan struct{}
}

type userContextKey struct{}

func currentUser(r *http.Request) auth.User { return r.Context().Value(userContextKey{}).(auth.User) }

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	return true
}

func (a *authHandler) cookie(token string) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: token, Path: "/api", HttpOnly: true,
		Secure: a.config.SecureCookies, SameSite: http.SameSiteStrictMode}
}
func (a *authHandler) clearCookie(w http.ResponseWriter) {
	cookie := a.cookie("")
	cookie.MaxAge = -1
	cookie.Expires = time.Unix(1, 0)
	http.SetCookie(w, cookie)
}

// All mutations, including login, require the configured browser origin.
// No proxy headers or client-supplied tenant/role are trusted.
func (a *authHandler) sameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != a.config.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, http.StatusForbidden, "Request origin is not allowed")
			return
		}
		next(w, r)
	}
}

func (a *authHandler) authenticated(next http.HandlerFunc, roles ...auth.Role) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil || !auth.ValidToken(cookie.Value) {
			writeError(w, http.StatusUnauthorized, "Sign in to continue")
			return
		}
		user, err := a.config.Store.ResolveSession(r.Context(), cookie.Value)
		if errors.Is(err, auth.ErrUnauthenticated) {
			a.clearCookie(w)
			writeError(w, http.StatusUnauthorized, "Your session has expired. Sign in again.")
			return
		}
		if err != nil {
			a.unavailable(w, err)
			return
		}
		if !user.Role.Valid() || (user.Role == auth.SystemAdministrator) != (user.OrganisationID == nil) {
			writeError(w, http.StatusForbidden, "Account access is not configured correctly")
			return
		}
		allowed := len(roles) == 0
		for _, role := range roles {
			if user.Role == role {
				allowed = true
			}
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "Your role cannot access this resource")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	}
}

func (a *authHandler) unavailable(w http.ResponseWriter, err error) {
	slog.Error("Authentication storage failed", "error", err)
	writeError(w, http.StatusServiceUnavailable, "Authentication is temporarily unavailable")
}

func (a *authHandler) login(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.allow(r.RemoteAddr) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.")
		return
	}
	select {
	case a.passwordSlots <- struct{}{}:
		defer func() { <-a.passwordSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "Authentication is busy. Try again shortly.")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	email, emailErr := auth.NormalizeEmail(input.Email)
	credentials, err := a.config.Store.FindCredentials(r.Context(), email)
	if err != nil && !errors.Is(err, auth.ErrUnauthenticated) {
		a.unavailable(w, err)
		return
	}
	hash := a.dummyHash
	if err == nil {
		hash = []byte(credentials.PasswordHash)
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(input.Password))
	if emailErr != nil || err != nil || passwordErr != nil || !credentials.Active || len(input.Password) > 72 {
		writeError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}
	token, err := auth.NewToken()
	if err != nil {
		a.unavailable(w, err)
		return
	}
	user, err := a.config.Store.CreateSession(r.Context(), credentials, token)
	if errors.Is(err, auth.ErrUnauthenticated) {
		writeError(w, http.StatusUnauthorized, "Invalid email or password")
		return
	}
	if err != nil {
		a.unavailable(w, err)
		return
	}
	// Re-login rotates the browser's previous session as well as the token.
	if previous, err := r.Cookie(cookieName); err == nil && auth.ValidToken(previous.Value) {
		if err := a.config.Store.DeleteSession(r.Context(), previous.Value); err != nil {
			_ = a.config.Store.DeleteSession(r.Context(), token)
			a.unavailable(w, err)
			return
		}
	}
	http.SetCookie(w, a.cookie(token))
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (a *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil && auth.ValidToken(cookie.Value) {
		if err := a.config.Store.DeleteSession(r.Context(), cookie.Value); err != nil {
			a.unavailable(w, err)
			return
		}
	}
	a.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *authHandler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": currentUser(r)})
}

func (a *authHandler) changePassword(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.allow(r.RemoteAddr) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.")
		return
	}
	select {
	case a.passwordSlots <- struct{}{}:
		defer func() { <-a.passwordSlots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "Authentication is busy. Try again shortly.")
		return
	}
	var input struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	user := currentUser(r)
	credentials, err := a.config.Store.FindCredentials(r.Context(), user.Email)
	if errors.Is(err, auth.ErrUnauthenticated) {
		writeError(w, http.StatusUnauthorized, "Sign in again")
		return
	}
	if err != nil {
		a.unavailable(w, err)
		return
	}
	if !credentials.Active || len(input.Current) > 72 || bcrypt.CompareHashAndPassword([]byte(credentials.PasswordHash), []byte(input.Current)) != nil {
		writeError(w, http.StatusBadRequest, "Current password is incorrect")
		return
	}
	if input.Current == input.New {
		writeError(w, http.StatusBadRequest, "Choose a different new password")
		return
	}
	hash, err := auth.HashPassword(input.New)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	err = a.config.Store.ChangePassword(r.Context(), user.ID, credentials.PasswordHash, hash)
	if errors.Is(err, auth.ErrUnauthenticated) {
		writeError(w, http.StatusUnauthorized, "Your account changed. Sign in again.")
		return
	}
	if err != nil {
		a.unavailable(w, err)
		return
	}
	a.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// Bounded per-process IP throttling. RemoteAddr is supplied by the HTTP server,
// never by X-Forwarded-For. Deployments with multiple replicas need a shared limiter.
type loginWindow struct {
	started  time.Time
	attempts int
}
type loginLimiter struct {
	mu      sync.Mutex
	windows map[string]loginWindow
}

func (l *loginLimiter) allow(remote string) bool {
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		ip = remote
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	window, exists := l.windows[ip]
	if !exists || now.Sub(window.started) >= time.Minute {
		for key, value := range l.windows {
			if now.Sub(value.started) >= time.Minute {
				delete(l.windows, key)
			}
		}
		if len(l.windows) >= 4096 {
			return false
		}
		window = loginWindow{started: now}
	}
	window.attempts++
	l.windows[ip] = window
	return window.attempts <= 10
}
