// Package credentials gives adapters a single way to obtain vendor secrets.
//
// Adapters depend only on Provider. Sprint 1 ships EnvProvider and
// StaticProvider. In Sprint 2 (NFR-04, "vendor credentials are encrypted at
// rest") a database-backed provider that decrypts values can implement the
// same interface, with no change to any adapter.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
)

// ErrNotFound is returned when no credentials exist for a reference.
var ErrNotFound = errors.New("credentials not found")

// Credentials is an opaque bag of named secrets (for example "api_key").
// It never prints its contents, so it is safe to pass to loggers by accident.
type Credentials struct {
	values map[string]string
}

// New copies m into a Credentials value.
func New(m map[string]string) Credentials {
	c := Credentials{values: make(map[string]string, len(m))}
	for k, v := range m {
		c.values[strings.ToLower(k)] = v
	}
	return c
}

// Get returns the secret stored under name (case-insensitive).
func (c Credentials) Get(name string) (string, bool) {
	v, ok := c.values[strings.ToLower(name)]
	return v, ok && v != ""
}

// Require returns the secret or an error naming the missing key (never the value).
func (c Credentials) Require(name string) (string, error) {
	if v, ok := c.Get(name); ok {
		return v, nil
	}
	return "", fmt.Errorf("credential %q is missing or empty", name)
}

// Keys lists the credential names (never values), sorted.
func (c Credentials) Keys() []string {
	keys := make([]string, 0, len(c.values))
	for k := range c.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// String, GoString and LogValue redact everything.
func (c Credentials) String() string   { return "Credentials{[REDACTED]}" }
func (c Credentials) GoString() string { return c.String() }
func (c Credentials) LogValue() slog.Value {
	return slog.StringValue("[REDACTED]")
}

// Provider looks up credentials by reference (for example "dji" or a vendor
// account id from the drone registry).
type Provider interface {
	Get(ctx context.Context, ref string) (Credentials, error)
}

// StaticProvider serves fixed credentials. Intended for tests and local demos.
type StaticProvider map[string]map[string]string

// Get implements Provider.
func (p StaticProvider) Get(_ context.Context, ref string) (Credentials, error) {
	m, ok := p[ref]
	if !ok {
		return Credentials{}, fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	return New(m), nil
}

// EnvProvider reads credentials from environment variables named
//
//	<Prefix><REF>_<KEY>      e.g. AEROSYNC_DJI_API_KEY  ->  ref "dji", key "api_key"
//
// Secrets stay out of the repository (R-12): set them in .env (git-ignored)
// or in the CI/secret store.
type EnvProvider struct {
	Prefix  string          // defaults to "AEROSYNC_"
	Environ func() []string // defaults to os.Environ; injectable for tests
}

// Get implements Provider.
func (p EnvProvider) Get(_ context.Context, ref string) (Credentials, error) {
	prefix := p.Prefix
	if prefix == "" {
		prefix = "AEROSYNC_"
	}
	environ := p.Environ
	if environ == nil {
		environ = os.Environ
	}
	full := strings.ToUpper(prefix + ref + "_")
	vals := map[string]string{}
	for _, kv := range environ() {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, full) || value == "" {
			continue
		}
		vals[strings.ToLower(strings.TrimPrefix(name, full))] = value
	}
	if len(vals) == 0 {
		return Credentials{}, fmt.Errorf("%w: no %s* variables set", ErrNotFound, full)
	}
	return New(vals), nil
}
