package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/fleetqa"
)

func registerFleetQA(mux *http.ServeMux, a *authHandler, service *fleetqa.Service) {
	mux.HandleFunc("POST /api/v1/fleet-qa", a.sameOrigin(a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Question string `json:"question"`
		}
		if !readJSONWithLimit(w, r, &input, 8<<10) {
			return
		}
		question := strings.TrimSpace(input.Question)
		if question == "" || utf8.RuneCountInString(question) > 500 || strings.ContainsRune(question, 0) {
			writeError(w, http.StatusBadRequest, "Enter a question of 1–500 characters")
			return
		}
		result, err := service.Ask(r.Context(), *currentUser(r).OrganisationID, question)
		if err != nil {
			slog.Error("Fleet Q&A failed", "error", err)
			result = fleetqa.Result{Status: "unavailable", Sources: []fleetqa.Source{}}
		}
		writeJSON(w, http.StatusOK, result)
	}, auth.FleetManager)))
}
