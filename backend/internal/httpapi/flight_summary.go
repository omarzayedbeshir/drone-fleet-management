package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/flightsummary"
)

func registerFlightSummary(mux *http.ServeMux, a *authHandler, service *flightsummary.Service) {
	mux.HandleFunc("POST /api/v1/flight-summary", a.sameOrigin(a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Day   string `json:"day"`
			UseAI bool   `json:"use_ai"`
		}
		if !readJSON(w, r, &input) {
			return
		}
		day, err := time.Parse(time.DateOnly, input.Day)
		if err != nil || day.Year() < 1970 || day.After(time.Now().UTC()) {
			writeError(w, http.StatusBadRequest, "Choose a UTC reporting date from 1970 through today")
			return
		}
		report, err := service.Generate(r.Context(), *currentUser(r).OrganisationID, day, input.UseAI)
		if err != nil {
			slog.Error("Read flight summary failed", "error", err)
			writeError(w, http.StatusServiceUnavailable, "Flight summary is temporarily unavailable")
			return
		}
		writeJSON(w, http.StatusOK, report)
	}, auth.FleetManager)))
}

func registerScheduledFlightSummary(mux *http.ServeMux, a *authHandler, schedule *flightsummary.PostgresSchedule) {
	mux.HandleFunc("GET /api/v1/flight-summary/schedule", a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		result, err := schedule.Read(r.Context(), *currentUser(r).OrganisationID)
		if err != nil {
			slog.Error("Read daily flight summary schedule failed", "error", err)
			writeError(w, http.StatusServiceUnavailable, "Daily flight summary settings are temporarily unavailable")
			return
		}
		writeJSON(w, http.StatusOK, result)
	}, auth.FleetManager))
	mux.HandleFunc("PUT /api/v1/flight-summary/schedule", a.sameOrigin(a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if !readJSON(w, r, &input) {
			return
		}
		if input.Enabled == nil {
			writeError(w, http.StatusBadRequest, "enabled is required")
			return
		}
		user := currentUser(r)
		if err := schedule.SetEnabled(r.Context(), *user.OrganisationID, user.ID, *input.Enabled); err != nil {
			slog.Error("Update daily flight summary schedule failed", "error", err)
			writeError(w, http.StatusServiceUnavailable, "Daily flight summary settings are temporarily unavailable")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}, auth.FleetManager)))
}
