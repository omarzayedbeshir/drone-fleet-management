package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func registerCycleBaselines(mux *http.ServeMux, a *authHandler, store telemetry.CycleBaselineStore) {
	mux.HandleFunc("GET /api/v1/drones/{id}/battery-cycles", a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		id, ok := droneID(w, r)
		if !ok {
			return
		}
		value, err := store.ReadCycleBaseline(r.Context(), *currentUser(r).OrganisationID, id)
		if err != nil {
			cycleBaselineError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"battery_cycles": value})
	}, auth.FleetManager, auth.Pilot, auth.MaintenanceTechnician))
	mux.HandleFunc("PUT /api/v1/drones/{id}/battery-cycles", a.sameOrigin(a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		id, ok := droneID(w, r)
		if !ok {
			return
		}
		var input struct {
			Total            *int64 `json:"total"`
			ExpectedTotal    *int64 `json:"expected_total"`
			ExpectedRevision *int64 `json:"expected_revision"`
			Reason           string `json:"reason"`
		}
		if !readJSON(w, r, &input) {
			return
		}
		if input.Total == nil || input.ExpectedTotal == nil || input.ExpectedRevision == nil {
			writeError(w, 400, "total, expected_total and expected_revision are required")
			return
		}
		reading := telemetry.CycleReading{Total: *input.Total, ExpectedTotal: *input.ExpectedTotal, ExpectedRevision: *input.ExpectedRevision, Reason: input.Reason}
		if err := reading.Validate(); err != nil {
			cycleBaselineError(w, err)
			return
		}
		u := currentUser(r)
		value, err := store.RecordCycleBaseline(r.Context(), *u.OrganisationID, u.ID, id, reading)
		if err != nil {
			cycleBaselineError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"battery_cycles": value})
	}, auth.FleetManager, auth.MaintenanceTechnician)))
}
func cycleBaselineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, telemetry.ErrNotFound):
		writeError(w, 404, "Drone not found")
	case errors.Is(err, telemetry.ErrInvalidCycleReading):
		writeError(w, 400, "Provide a whole-number cycle total from 0 to 1000000000 and a 5–500 character source/reference")
	case errors.Is(err, telemetry.ErrCycleConflict):
		writeError(w, 409, "Cycle history changed or conflicts with this reading. Reload and verify the total before trying again.")
	case errors.Is(err, telemetry.ErrCyclePermission):
		writeError(w, 403, "An active fleet manager or maintenance technician is required")
	default:
		slog.Error("Battery cycle baseline storage failed")
		writeError(w, 503, "Battery cycle baseline is temporarily unavailable")
	}
}
