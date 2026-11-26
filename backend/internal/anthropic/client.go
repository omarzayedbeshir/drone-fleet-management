package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const messagesURL = "https://api.anthropic.com/v1/messages"

type Client struct {
	APIKey string
	Model  string
	Client *http.Client
}

func (w Client) Complete(ctx context.Context, system, input string, maxTokens int) (string, error) {
	return w.complete(ctx, system, input, maxTokens, nil, 6*time.Second)
}

func (w Client) CompleteJSON(ctx context.Context, system, input string, maxTokens int, schema json.RawMessage) (string, error) {
	return w.complete(ctx, system, input, maxTokens, schema, 8*time.Second)
}

func (w Client) complete(ctx context.Context, system, input string, maxTokens int, schema json.RawMessage, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload := map[string]any{
		"model": w.Model, "max_tokens": maxTokens, "system": system,
		"messages": []map[string]string{{"role": "user", "content": input}},
	}
	if len(schema) > 0 {
		config := map[string]any{"format": map[string]any{"type": "json_schema", "schema": schema}}
		if w.Model == "claude-haiku-5-5" {
			config["effort"] = "low"
		}
		payload["output_config"] = config
	}
	body, err := json.Marshal(payload)
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
		return "", fmt.Errorf("Anthropic returned HTTP %d", response.StatusCode)
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
		return "", fmt.Errorf("Anthropic stopped with reason %q", result.StopReason)
	}
	var paragraphs []string
	for _, part := range result.Content {
		if text := strings.TrimSpace(part.Text); part.Type == "text" && text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	if len(paragraphs) == 0 {
		return "", errors.New("Anthropic returned no text")
	}
	return strings.Join(paragraphs, "\n"), nil
}
