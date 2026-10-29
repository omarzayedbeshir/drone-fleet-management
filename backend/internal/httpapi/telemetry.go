package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

func registerTelemetry(mux *http.ServeMux, a *authHandler, store telemetry.Store) {
	mux.HandleFunc("POST /api/v1/drones/{id}/telemetry", a.sameOrigin(a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		id, ok := droneID(w, r)
		if !ok {
			return
		}
		if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
			writeError(w, 415, "Content-Type must be application/json")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, telemetry.MaxBody+1))
		if err != nil {
			writeError(w, 400, "Could not read telemetry body")
			return
		}
		user := currentUser(r)
		result, err := store.Ingest(r.Context(), *user.OrganisationID, user.ID, id, raw)
		if err != nil {
			telemetryError(w, err)
			return
		}
		status := http.StatusCreated
		if result.Duplicate {
			status = http.StatusOK
		}
		writeJSON(w, status, result)
	}, auth.FleetManager)))
	mux.HandleFunc("GET /api/v1/drones/{id}/telemetry", a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		id, ok := droneID(w, r)
		if !ok {
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeError(w, 400, "Invalid query parameters")
			return
		}
		limit, offset := 20, 0
		for key, values := range query {
			if len(values) != 1 {
				writeError(w, 400, "Query parameters must not be repeated")
				return
			}
			n, err := strconv.Atoi(values[0])
			if err != nil {
				writeError(w, 400, "Invalid pagination")
				return
			}
			switch key {
			case "limit":
				if n < 1 || n > 100 {
					writeError(w, 400, "limit must be 1–100")
					return
				}
				limit = n
			case "offset":
				if n < 0 || n > 1000000 {
					writeError(w, 400, "offset must be 0–1000000")
					return
				}
				offset = n
			default:
				writeError(w, 400, "Unsupported query parameter")
				return
			}
		}
		samples, more, err := store.List(r.Context(), *currentUser(r).OrganisationID, id, limit, offset)
		if err != nil {
			telemetryError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"samples": samples, "pagination": map[string]any{"limit": limit, "offset": offset, "has_more": more}})
	}, auth.FleetManager, auth.Pilot, auth.MaintenanceTechnician))
}
func telemetryError(w http.ResponseWriter, err error) {
	if errors.Is(err, telemetry.ErrNotFound) {
		writeError(w, 404, "Drone not found")
		return
	}
	var rejection *telemetry.Rejection
	if errors.As(err, &rejection) {
		status := 422
		switch rejection.Code {
		case "event_conflict", "timestamp_conflict":
			status = 409
		case "body_too_large":
			status = 413
		}
		writeJSON(w, status, map[string]string{"error": rejection.Message, "code": rejection.Code})
		return
	}
	slog.Error("Telemetry storage failed", "error", err)
	writeError(w, 503, "Telemetry ingestion is temporarily unavailable")
}
