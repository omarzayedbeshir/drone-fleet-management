package httpapi

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
)

func registerAuditHistory(mux *http.ServeMux, a *authHandler, reader audit.HistoryReader) {
	mux.HandleFunc("GET /api/v1/audit-events", a.authenticated(func(w http.ResponseWriter, r *http.Request) {
		listAuditHistory(w, r, reader)
	}, auth.FleetManager, auth.SystemAdministrator))
}

func listAuditHistory(w http.ResponseWriter, r *http.Request, reader audit.HistoryReader) {
	user := currentUser(r)
	limit := 25
	var filter audit.HistoryFilter
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
		value := values[0]
		switch key {
		case "organisation_id":
			if user.Role != auth.SystemAdministrator {
				writeError(w, http.StatusForbidden, "Audit history uses your organisation")
				return
			}
			if !audit.ValidID(value) {
				writeError(w, http.StatusBadRequest, "organisation_id must be a UUID")
				return
			}
			filter.OrganisationID = value
		case "resource_type":
			if !audit.ValidResourceType(value) {
				writeError(w, http.StatusBadRequest, "Invalid resource_type")
				return
			}
			filter.ResourceType = value
		case "before_id":
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id < 1 {
				writeError(w, http.StatusBadRequest, "before_id must be a positive event ID")
				return
			}
			filter.BeforeID = &id
		case "limit":
			count, err := strconv.Atoi(value)
			if err != nil || count < 1 || count > 100 {
				writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
				return
			}
			limit = count
		default:
			writeError(w, http.StatusBadRequest, "Unsupported query parameter")
			return
		}
	}
	if user.OrganisationID != nil {
		filter.OrganisationID = *user.OrganisationID
	}
	if filter.OrganisationID == "" {
		writeError(w, http.StatusBadRequest, "organisation_id is required for system administrators")
		return
	}
	filter.Limit = limit + 1
	entries, err := reader.List(r.Context(), filter)
	if err != nil {
		slog.Error("Read audit history failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "Audit history is temporarily unavailable")
		return
	}
	var next *string
	if len(entries) > limit {
		entries = entries[:limit]
		cursor := strconv.FormatInt(entries[limit-1].ID, 10)
		next = &cursor
	}
	writeJSON(w, http.StatusOK, struct {
		Events []audit.HistoryEntry `json:"events"`
		Next   *string              `json:"next_before_id"`
	}{entries, next})
}
