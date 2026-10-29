package httpapi

import (
	"encoding/json"
	"net/http"
)

// NewHandler exposes only public liveness checks. Add authentication and
// organisation scoping before registering application endpoints.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Status  string `json:"status"`
			Service string `json:"service"`
		}{Status: "ok", Service: "aerosync-api"})
	})
	return mux
}
