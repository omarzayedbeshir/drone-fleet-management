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
