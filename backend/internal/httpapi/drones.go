package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
)

type droneHandler struct{ store drones.Store }

func registerDrones(mux *http.ServeMux, a *authHandler, store drones.Store) {
	d := &droneHandler{store: store}
	readers := []auth.Role{auth.FleetManager, auth.Pilot, auth.MaintenanceTechnician}
	mux.HandleFunc("GET /api/v1/drones", a.authenticated(d.list, readers...))
	mux.HandleFunc("GET /api/v1/drones/{id}", a.authenticated(d.get, readers...))
	mux.HandleFunc("POST /api/v1/drones", a.sameOrigin(a.authenticated(d.create, auth.FleetManager)))
	mux.HandleFunc("PATCH /api/v1/drones/{id}", a.sameOrigin(a.authenticated(d.update, auth.FleetManager)))
	mux.HandleFunc("DELETE /api/v1/drones/{id}", a.sameOrigin(a.authenticated(d.delete, auth.FleetManager)))
}

func droneID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	var parsed pgtype.UUID
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || parsed.Scan(id) != nil || !parsed.Valid {
		writeError(w, http.StatusBadRequest, "Drone ID must be a UUID")
		return "", false
	}
	return id, true
}

func droneError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, drones.ErrNotFound):
		writeError(w, http.StatusNotFound, "Drone not found")
	case errors.Is(err, drones.ErrConflict):
		writeError(w, http.StatusConflict, "Drone conflicts with an existing serial number or a related record")
	default:
		slog.Error("Drone registry storage failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "Drone registry is temporarily unavailable")
	}
}

func (d *droneHandler) create(w http.ResponseWriter, r *http.Request) {
	var input drones.Metadata
	if !readJSON(w, r, &input) {
		return
	}
	if err := input.NormalizeValidate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	user := currentUser(r)
	drone, err := d.store.Create(r.Context(), *user.OrganisationID, user.ID, input)
	if err != nil {
		droneError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/drones/"+drone.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"drone": drone})
}
func (d *droneHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := droneID(w, r)
	if !ok {
		return
	}
	drone, err := d.store.Get(r.Context(), *currentUser(r).OrganisationID, id)
	if err != nil {
		droneError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"drone": drone})
}
func (d *droneHandler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := droneID(w, r)
	if !ok {
		return
	}
	var input drones.Patch
	if !readJSON(w, r, &input) {
		return
	}
	if err := input.NormalizeValidate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	user := currentUser(r)
	drone, err := d.store.Update(r.Context(), *user.OrganisationID, user.ID, id, input)
	if err != nil {
		droneError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"drone": drone})
}
func (d *droneHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := droneID(w, r)
	if !ok {
		return
	}
	user := currentUser(r)
	if err := d.store.Delete(r.Context(), *user.OrganisationID, user.ID, id); err != nil {
		droneError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (d *droneHandler) list(w http.ResponseWriter, r *http.Request) {
	f := drones.Filter{Limit: 20}
	query, err := parseDroneFilter(r, &f)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	f.Query = query
	items, more, err := d.store.List(r.Context(), *currentUser(r).OrganisationID, f)
	if err != nil {
		droneError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"drones": items, "pagination": map[string]any{"limit": f.Limit, "offset": f.Offset, "has_more": more}})
}

func parseDroneFilter(r *http.Request, f *drones.Filter) (string, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", err
	}
	for key, values := range query {
		if len(values) != 1 {
			return "", errors.New("query parameters must not be repeated")
		}
		value := values[0]
		switch key {
		case "limit":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 100 {
				return "", errors.New("limit must be between 1 and 100")
			}
			f.Limit = n
		case "offset":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 1000000 {
				return "", errors.New("offset must be between 0 and 1000000")
			}
			f.Offset = n
		case "vendor":
			if !drones.ValidVendor(value) {
				return "", errors.New("vendor must be dji or ardupilot")
			}
			f.Vendor = value
		case "status":
			if !drones.ValidStatus(value) {
				return "", errors.New("invalid drone status")
			}
			f.Status = value
		case "q":
			value = strings.TrimSpace(value)
			if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 200 || strings.ContainsRune(value, 0) {
				return "", errors.New("q must contain at most 200 characters and no null characters")
			}
			f.Query = value
		default:
			return "", errors.New("unsupported query parameter: " + key)
		}
	}
	return f.Query, nil
}
