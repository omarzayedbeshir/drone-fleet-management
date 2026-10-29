package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	registerHealth(mux)
	return mux
}

func registerHealth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "aerosync-api"})
	})
}

func NewAuthenticatedHandler(config AuthConfig) (http.Handler, error) {
	origin, err := url.Parse(config.Origin)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" ||
		(origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, errors.New("APP_ORIGIN must be an exact http(s) origin without a trailing slash")
	}
	if config.SecureCookies && origin.Scheme != "https" {
		return nil, errors.New("secure cookies require an HTTPS APP_ORIGIN")
	}
	if !config.SecureCookies && origin.Hostname() != "localhost" && origin.Hostname() != "127.0.0.1" && origin.Hostname() != "::1" {
		return nil, errors.New("insecure cookies are allowed only for a localhost APP_ORIGIN")
	}
	if config.Store == nil {
		return nil, errors.New("authentication store is required")
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("unknown-account-dummy-password"), 12)
	if err != nil {
		return nil, err
	}
	a := &authHandler{config: config, dummyHash: dummy, passwordSlots: make(chan struct{}, 4), limiter: &loginLimiter{windows: make(map[string]loginWindow)}}
	mux := http.NewServeMux()
	registerHealth(mux)
	mux.HandleFunc("POST /api/v1/auth/login", a.sameOrigin(a.login))
	mux.HandleFunc("POST /api/v1/auth/logout", a.sameOrigin(a.logout))
	mux.HandleFunc("GET /api/v1/auth/me", a.authenticated(a.me))
	mux.HandleFunc("POST /api/v1/auth/password", a.sameOrigin(a.authenticated(a.changePassword)))
	if config.Registry != nil {
		registerDrones(mux, a, config.Registry)
	}
	if config.Telemetry != nil {
		registerTelemetry(mux, a, config.Telemetry)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}
