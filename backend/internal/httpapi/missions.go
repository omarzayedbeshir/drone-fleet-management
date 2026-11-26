package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/missions"
)

type missionHandler struct{ store missions.Store }

func registerMissions(mux *http.ServeMux, a *authHandler, store missions.Store) {
	m := missionHandler{store: store}
	roles := []auth.Role{auth.FleetManager, auth.Pilot}
	mux.HandleFunc("GET /api/v1/missions", a.authenticated(m.list, roles...))
	mux.HandleFunc("GET /api/v1/missions/assignment-options", a.authenticated(m.options, roles...))
	mux.HandleFunc("POST /api/v1/missions", a.sameOrigin(a.authenticated(m.save, roles...)))
	mux.HandleFunc("PUT /api/v1/missions/{id}", a.sameOrigin(a.authenticated(m.save, roles...)))
	mux.HandleFunc("POST /api/v1/missions/{id}/cancel", a.sameOrigin(a.authenticated(m.cancel, roles...)))
}

func (m missionHandler) options(w http.ResponseWriter, r *http.Request) {
	options, err := m.store.Options(r.Context(), currentUser(r))
	if err != nil {
		missionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

func (m missionHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, offset := 20, 0
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid query parameters")
		return
	}
	for key, values := range query {
		if len(values) != 1 {
			writeError(w, http.StatusBadRequest, "Query parameters must not be repeated")
			return
		}
		value, err := strconv.Atoi(values[0])
		switch {
		case key == "limit" && err == nil && value >= 1 && value <= 100:
			limit = value
		case key == "offset" && err == nil && value >= 0 && value <= 1000000:
			offset = value
		default:
			writeError(w, http.StatusBadRequest, "Use limit between 1 and 100 and offset between 0 and 1000000")
			return
		}
	}
	items, err := m.store.List(r.Context(), currentUser(r), limit+1, offset)
	if err != nil {
		missionError(w, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"missions": items, "pagination": map[string]any{"limit": limit, "offset": offset, "has_more": more},
	})
}

func (m missionHandler) save(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodPut && !missions.ValidID(id) {
		writeError(w, http.StatusBadRequest, "Mission ID must be a UUID")
		return
	}
	var input missions.Input
	if !readJSONWithLimit(w, r, &input, 32<<10) {
		return
	}
	if err := input.Normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mission, err := m.store.Save(r.Context(), currentUser(r), id, input)
	if err != nil {
		missionError(w, err)
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"mission": mission})
}

func (m missionHandler) cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !missions.ValidID(id) {
		writeError(w, http.StatusBadRequest, "Mission ID must be a UUID")
		return
	}
	if err := m.store.Cancel(r.Context(), currentUser(r), id); err != nil {
		missionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func missionError(w http.ResponseWriter, err error) {
	var domainError missions.Error
	if !errors.As(err, &domainError) {
		slog.Error("Mission storage failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "Mission scheduling is temporarily unavailable")
		return
	}
	status := http.StatusConflict
	switch domainError {
	case missions.ErrNotFound:
		status = http.StatusNotFound
	case missions.ErrForbidden:
		status = http.StatusForbidden
	case missions.ErrAssignment:
		status = http.StatusBadRequest
	}
	writeError(w, status, domainError.Error())
}
