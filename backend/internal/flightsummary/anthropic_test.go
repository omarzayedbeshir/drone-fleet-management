package flightsummary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAnthropicWriterSendsOnlyCounts(t *testing.T) {
	want := Facts{Counts: Counts{Observations: 12, InMission: 3}, Aircraft: 2, AircraftInMission: 1}
	writer := AnthropicWriter{APIKey: "test-key", Model: "test-model", Client: &http.Client{Transport: transportFunc(
		func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost || r.URL.String() != messagesURL ||
				r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Anthropic-Version") != "2026-10-21" {
				t.Fatalf("incorrect Anthropic request: %s %s", r.Method, r.URL)
			}
			var payload struct {
				Model    string
				Messages []struct{ Role, Content string }
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			if payload.Model != "test-model" || len(payload.Messages) != 1 ||
				payload.Messages[0].Role != "user" || payload.Messages[0].Content != string(encoded) {
				t.Fatalf("expected only aggregate counts: %+v", payload)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
				`{"stop_reason":"end_turn","content":[{"type":"text","text":"One aircraft reported in-mission activity."}]}`))}, nil
		},
	)}}
	text, err := writer.Narrate(context.Background(), want)
	if err != nil || text != "One aircraft reported in-mission activity." {
		t.Fatalf("unexpected result: %q, %v", text, err)
	}
}
