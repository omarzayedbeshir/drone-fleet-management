// Package adapters defines the normalized, privacy-filtered vendor boundary.
package adapters

import (
	"context"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/telemetry"
)

type Emit func(context.Context, telemetry.Sample) error

type Health struct {
	Ready         bool      `json:"ready"`
	Connected     bool      `json:"connected"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	LastFrame     time.Time `json:"last_frame"`
	LastEmitted   time.Time `json:"last_emitted"`
	Accepted      uint64    `json:"accepted"`
	Rejected      uint64    `json:"rejected"`
	Dropped       uint64    `json:"dropped"`
	ParseErrors   uint64    `json:"parse_errors"`
	LastError     string    `json:"last_error,omitempty"`
}

// VendorAdapter streams only explicit normalized fields, never arbitrary vendor
// payloads. Implementations own connection lifecycle and health reporting.
type VendorAdapter interface {
	Run(context.Context, Emit) error
	Health() Health
}

// PermanentError drops a rejected record instead of retrying it forever.
// Future records are still processed, allowing operator correction/recovery.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }
