package flightsummary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const messagesURL = "https://api.anthropic.com/v1/messages"

type AnthropicWriter struct {
	APIKey string
	Model  string
	Client *http.Client
}

func (w AnthropicWriter) Narrate(ctx context.Context, facts Facts) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	input, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"model": w.Model, "max_tokens": 256,
		"system":   "Write a two-sentence plain-text summary using only the supplied telemetry counts. An in-mission observation is a reported status, not a distinct flight. Do not infer flight counts, duration, causes, or operational advice.",
		"messages": []map[string]string{{"role": "user", "content": string(input)}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, messagesURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+w.APIKey)
	req.Header.Set("Anthropic-Version", "2026-10-21")
	req.Header.Set("Content-Type", "application/json")
	client := w.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("Anthropic summary request failed")
	}
	var result struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return "", err
	}
	if result.StopReason != "end_turn" {
		return "", errors.New("Anthropic did not complete the summary")
	}
	var paragraphs []string
	for _, part := range result.Content {
		if text := strings.TrimSpace(part.Text); part.Type == "text" && text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	if len(paragraphs) == 0 {
		return "", errors.New("Anthropic returned no summary text")
	}
	return strings.Join(paragraphs, "\n"), nil
}
