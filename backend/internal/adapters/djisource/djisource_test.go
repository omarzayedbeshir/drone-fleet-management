package djisource

import (
	"context"
	"errors"
	"testing"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/csvreplay"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/adapters/dji"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/credentials"
)

var sample = csvreplay.Config{Path: "../../../testdata/dji_sample.csv"}

func TestLiveWhenCredentialsExist(t *testing.T) {
	sel, err := Select(context.Background(), dji.Config{
		BaseURL:     "https://example.invalid",
		Credentials: credentials.StaticProvider{"dji": {"api_key": "k"}},
	}, sample)
	if err != nil || sel.Simulated || sel.Adapter.Name() != "dji" {
		t.Fatalf("sel=%+v err=%v", sel, err)
	}
}

func TestFallsBackToSimulated(t *testing.T) {
	cases := map[string]dji.Config{
		"no base url":    {Credentials: credentials.StaticProvider{"dji": {"api_key": "k"}}},
		"no provider":    {BaseURL: "https://example.invalid"},
		"no credentials": {BaseURL: "https://example.invalid", Credentials: credentials.StaticProvider{}},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			sel, err := Select(context.Background(), cfg, sample)
			if err != nil || !sel.Simulated || sel.Reason == "" {
				t.Fatalf("sel=%+v err=%v", sel, err)
			}
			if sel.Adapter.Name() != "csv" {
				t.Errorf("name = %s", sel.Adapter.Name())
			}
		})
	}
}

type brokenProvider struct{}

func (brokenProvider) Get(context.Context, string) (credentials.Credentials, error) {
	return credentials.Credentials{}, errors.New("secret store unreachable")
}

func TestSecretStoreErrorIsNotHiddenAsSimulation(t *testing.T) {
	_, err := Select(context.Background(), dji.Config{BaseURL: "https://example.invalid", Credentials: brokenProvider{}}, sample)
	if err == nil {
		t.Fatal("a broken secret store must not silently fall back to simulated data")
	}
}

func TestBadSampleConfigIsReported(t *testing.T) {
	if _, err := Select(context.Background(), dji.Config{}, csvreplay.Config{}); err == nil {
		t.Fatal("expected error for sample config without a path")
	}
}
