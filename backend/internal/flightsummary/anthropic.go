package flightsummary

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/anthropic"
)

type AnthropicWriter struct {
	APIKey string
	Model  string
	Client *http.Client
}

func (w AnthropicWriter) Narrate(ctx context.Context, facts Facts) (string, error) {
	input, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	client := anthropic.Client{APIKey: w.APIKey, Model: w.Model, Client: w.Client}
	return client.Complete(ctx, "Write a two-sentence plain-text summary using only the supplied telemetry counts. An in-mission observation is a reported status, not a distinct flight. Do not infer flight counts, duration, causes, or operational advice.", string(input), 256)
}
