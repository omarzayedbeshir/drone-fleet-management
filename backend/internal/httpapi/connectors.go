package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/connectors"
)

func registerConnectors(mux *http.ServeMux, a *authHandler, store connectors.Store) {
	mux.HandleFunc("GET /api/v1/drones/{id}/connector", a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		id, ok := droneID(w, r)
		if !ok {
			return
		}
		view, err := store.Get(r.Context(), *currentUser(r).OrganisationID, id)
		if err != nil {
			connectorError(w, err)
			return
		}
		writeJSON(w, 200, view)
	}, auth.FleetManager, auth.Pilot, auth.MaintenanceTechnician))
	mux.HandleFunc("PUT /api/v1/drones/{id}/connector", a.sameOrigin(a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		id, ok := droneID(w, r)
		if !ok {
			return
		}
		var input struct {
			ProfileID string `json:"profile_id"`
			Enabled   *bool  `json:"enabled"`
		}
		if !readJSON(w, r, &input) {
			return
		}
		if input.Enabled == nil || input.ProfileID == "" {
			writeError(w, 400, "profile_id and enabled are required")
			return
		}
		u := currentUser(r)
		view, err := store.Save(r.Context(), *u.OrganisationID, u.ID, id, connectors.Settings{ProfileID: input.ProfileID, Enabled: *input.Enabled})
		if err != nil {
			connectorError(w, err)
			return
		}
		writeJSON(w, 200, view)
	}, auth.FleetManager)))
}
func connectorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, connectors.ErrNotFound):
		writeError(w, 404, "Drone not found")
	case errors.Is(err, connectors.ErrInvalid):
		writeError(w, 400, "Connection profile is unavailable for this aircraft")
	case errors.Is(err, connectors.ErrInactive):
		writeError(w, 403, "An active fleet manager is required")
	default:
		slog.Error("Connector storage failed")
		writeError(w, 503, "Connector management is temporarily unavailable")
	}
}
