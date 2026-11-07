// Package dji is the DJI Cloud API adapter (section 4.2, FR-08).
//
// It polls a REST endpoint per drone, converts each response into a
// vendor.RawRecord, and reports health. It does NOT normalize anything: the
// payload keeps DJI-native field names (nested objects are flattened with "_",
// see rawutil.Flatten) so that a recorded DJI-format sample replayed through
// the CSV adapter produces identical records (R-02 fallback).
//
// >>> IMPORTANT: the endpoint paths, auth header and response envelope below
// >>> are CONFIGURABLE ASSUMPTIONS modelled on DJI's Cloud API style
// >>> ({"code":0,"message":"ok","data":{...}}). Hamza must confirm them against
// >>> the access DJI actually granted and adjust the defaults in Config (or
// >>> the two unexported request functions) accordingly. Nothing else in the
// >>> pipeline depends on them.
//
// Retry policy: this adapter does not implement exponential backoff itself
// (that is the connector manager's job, NFR-03). It tolerates a few failed
// poll cycles, then returns an error from Subscribe so the manager can back
// off and reconnect. Authentication failures wrap vendor.ErrPermanent so the
// manager does not hammer the vendor with bad credentials.
package dji

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/credentials"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/rawutil"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/vendor"
)

// VendorName is the value of RawRecord.Vendor for DJI data.
const VendorName = "dji"

const maxBody = 1 << 20 // 1 MiB: a telemetry snapshot is a few hundred bytes

// ErrAuth is returned for HTTP 401/403. It also wraps vendor.ErrPermanent.
var ErrAuth = errors.New("dji: authentication rejected")

// Config configures the adapter.
type Config struct {
	// BaseURL of the API, e.g. "https://<region-host>" (no trailing slash needed).
	BaseURL string
	// Credentials supplies secrets. Required. See package credentials.
	Credentials credentials.Provider
	// CredentialRef selects the credential set. Default "dji".
	CredentialRef string
	// TokenKey is the credential holding the API token. Default "api_key".
	TokenKey string
	// AuthHeader carries the token. Default "X-User-Token".
	AuthHeader string
	// DeviceSerials to poll. Empty = discover them from DevicesPath on Connect.
	DeviceSerials []string
	// DevicesPath lists devices: {"data":[{"device_sn":"..."}]}. Default "/devices".
	DevicesPath string
	// TelemetryPath is a format string with one %s for the serial.
	// Default "/devices/%s/telemetry".
	TelemetryPath string
	// PollInterval between poll cycles. Default 2s.
	PollInterval time.Duration
	// RequestTimeout per HTTP call. Default 10s.
	RequestTimeout time.Duration
	// MaxConsecutiveFailures failed cycles before Subscribe returns an error.
	// Default 3.
	MaxConsecutiveFailures int
	// HTTPClient overrides the default client (tests, proxies).
	HTTPClient *http.Client
	// Now is the clock. Default time.Now.
	Now func() time.Time
}

// Adapter implements vendor.VendorAdapter for DJI.
type Adapter struct {
	cfg    Config
	client *http.Client

	mu      sync.Mutex
	token   string
	devices []string
	health  vendor.Health
}

// New validates cfg and builds an Adapter. It performs no network I/O.
func New(cfg Config) (*Adapter, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("dji: BaseURL is required: %w", vendor.ErrPermanent)
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("dji: BaseURL %q is not a valid http(s) URL: %w", cfg.BaseURL, vendor.ErrPermanent)
	}
	if cfg.Credentials == nil {
		return nil, fmt.Errorf("dji: a credentials.Provider is required: %w", vendor.ErrPermanent)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.CredentialRef == "" {
		cfg.CredentialRef = "dji"
	}
	if cfg.TokenKey == "" {
		cfg.TokenKey = "api_key"
	}
	if cfg.AuthHeader == "" {
		cfg.AuthHeader = "X-User-Token"
	}
	if cfg.DevicesPath == "" {
		cfg.DevicesPath = "/devices"
	}
	if cfg.TelemetryPath == "" {
		cfg.TelemetryPath = "/devices/%s/telemetry"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 10 * time.Second
	}
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = 3
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &Adapter{
		cfg:     cfg,
		client:  client,
		devices: append([]string(nil), cfg.DeviceSerials...),
		health:  vendor.Health{State: vendor.HealthUnknown},
	}, nil
}

// Name implements vendor.VendorAdapter.
func (a *Adapter) Name() string { return VendorName }

// Connect loads credentials, checks that the API answers and the token is
// accepted, and discovers devices if none were configured.
func (a *Adapter) Connect(ctx context.Context) error {
	creds, err := a.cfg.Credentials.Get(ctx, a.cfg.CredentialRef)
	if err != nil {
		a.fail(vendor.HealthDown, err)
		if errors.Is(err, credentials.ErrNotFound) {
			return fmt.Errorf("dji: %w: %w", err, vendor.ErrPermanent)
		}
		return fmt.Errorf("dji: loading credentials: %w", err)
	}
	token, err := creds.Require(a.cfg.TokenKey)
	if err != nil {
		a.fail(vendor.HealthDown, err)
		return fmt.Errorf("dji: %w: %w", err, vendor.ErrPermanent)
	}
	a.mu.Lock()
	a.token = token
	a.mu.Unlock()

	var list []struct {
		SN string `json:"device_sn"`
	}
	if err := a.get(ctx, a.cfg.DevicesPath, &list); err != nil {
		a.fail(vendor.HealthDown, err)
		return err
	}
	a.mu.Lock()
	if len(a.devices) == 0 {
		for _, d := range list {
			if d.SN != "" {
				a.devices = append(a.devices, d.SN)
			}
		}
	}
	n := len(a.devices)
	a.mu.Unlock()
	if n == 0 {
		err := fmt.Errorf("dji: no devices configured or discovered: %w", vendor.ErrPermanent)
		a.fail(vendor.HealthDown, err)
		return err
	}
	a.touch(vendor.HealthHealthy)
	return nil
}

// Subscribe polls every device each PollInterval until ctx is cancelled, a
// permanent error occurs, or MaxConsecutiveFailures cycles fail in a row.
func (a *Adapter) Subscribe(ctx context.Context, out chan<- vendor.RawRecord) error {
	a.mu.Lock()
	devices := append([]string(nil), a.devices...)
	a.mu.Unlock()
	if len(devices) == 0 {
		return fmt.Errorf("dji: Subscribe called before a successful Connect: %w", vendor.ErrPermanent)
	}

	ticker := time.NewTicker(a.cfg.PollInterval)
	defer ticker.Stop()

	consecutive := 0
	for {
		okCount, failCount := 0, 0
		var lastErr error
		for _, sn := range devices {
			payload, err := a.fetchTelemetry(ctx, sn)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(err, vendor.ErrPermanent) {
					a.fail(vendor.HealthDown, err)
					return err
				}
				slog.Warn("dji: poll failed", "device", sn, "err", err)
				failCount++
				lastErr = err
				continue
			}
			okCount++
			rec := vendor.RawRecord{
				Vendor:        VendorName,
				SourceDroneID: sn,
				ReceivedAt:    a.cfg.Now().UTC(),
				Simulated:     false,
				Payload:       payload,
			}
			select {
			case out <- rec:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		switch {
		case okCount > 0 && failCount == 0:
			consecutive = 0
			a.touch(vendor.HealthHealthy)
		case okCount > 0:
			consecutive = 0
			a.touch(vendor.HealthDegraded)
			a.setError(lastErr)
		default:
			consecutive++
			a.fail(vendor.HealthDegraded, lastErr)
			if consecutive >= a.cfg.MaxConsecutiveFailures {
				a.fail(vendor.HealthDown, lastErr)
				return fmt.Errorf("dji: %d consecutive failed poll cycles: %w", consecutive, lastErr)
			}
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Health implements vendor.VendorAdapter.
func (a *Adapter) Health() vendor.Health {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.health
}

// Close implements vendor.VendorAdapter. Safe to call more than once.
func (a *Adapter) Close() error {
	a.client.CloseIdleConnections()
	a.mu.Lock()
	a.token = "" // drop the secret from memory references we hold
	a.mu.Unlock()
	return nil
}

var _ vendor.VendorAdapter = (*Adapter)(nil)

// ---- HTTP ----

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (a *Adapter) fetchTelemetry(ctx context.Context, sn string) (map[string]any, error) {
	path := fmt.Sprintf(a.cfg.TelemetryPath, url.PathEscape(sn))
	var data map[string]any
	if err := a.get(ctx, path, &data); err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("dji: telemetry for %s is not a JSON object", sn)
	}
	return rawutil.Flatten(data), nil
}

// get performs one authenticated GET and decodes envelope.data into dst.
func (a *Adapter) get(ctx context.Context, path string, dst any) error {
	a.mu.Lock()
	token := a.token
	a.mu.Unlock()
	if token == "" {
		return fmt.Errorf("dji: not connected: %w", vendor.ErrPermanent)
	}

	reqCtx, cancel := context.WithTimeout(ctx, a.cfg.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, a.cfg.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("dji: building request: %w: %w", err, vendor.ErrPermanent)
	}
	req.Header.Set(a.cfg.AuthHeader, token)
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		// err can embed the URL but never the token (it is in a header).
		return fmt.Errorf("dji: request failed: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (HTTP %d): %w", ErrAuth, resp.StatusCode, vendor.ErrPermanent)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("dji: HTTP %d from %s", resp.StatusCode, path)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("dji: reading response: %w", err)
	}
	if len(body) > maxBody {
		return fmt.Errorf("dji: response from %s exceeds %d bytes", path, maxBody)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("dji: malformed JSON from %s: %w", path, err)
	}
	if env.Code != 0 {
		return fmt.Errorf("dji: API error code %d from %s: %s", env.Code, path, env.Message)
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		return fmt.Errorf("dji: unexpected data shape from %s: %w", path, err)
	}
	return nil
}

// ---- health bookkeeping ----

func (a *Adapter) touch(state vendor.HealthState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.health.State = state
	a.health.LastContact = a.cfg.Now().UTC()
	if state == vendor.HealthHealthy {
		a.health.LastError = ""
	}
}

func (a *Adapter) setError(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	a.health.LastError = err.Error()
	a.mu.Unlock()
}

func (a *Adapter) fail(state vendor.HealthState, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.health.State = state
	if err != nil {
		a.health.LastError = err.Error()
	}
}
