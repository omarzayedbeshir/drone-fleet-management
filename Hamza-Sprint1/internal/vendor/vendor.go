// Package vendor defines the single contract every vendor adapter implements
// (NFR-07).
//
// OWNER: Seif. This file is a MINIMAL STUB written by Hamza so that the
// CSV, DJI and mock adapters compile and can be tested before Seif's real
// interface lands. When Seif merges the real package, replace this file and
// fix any signature differences (they should be mechanical). The shapes below
// follow section 4.2 of the Milestone 1 document: connect, fetch/subscribe,
// emit raw records, report health.
package vendor

import (
	"context"
	"errors"
	"time"
)

// RawRecord is one vendor-native telemetry message exactly as the adapter
// produced it. It has NOT been privacy-filtered or normalized yet: the
// privacy filter (SOC-03) and normalizer (FR-09) run after this.
type RawRecord struct {
	// Vendor is the adapter's Name(), e.g. "dji", "mavlink", "csv".
	Vendor string
	// SourceDroneID is the vendor-side identifier (serial number, system id).
	SourceDroneID string
	// ReceivedAt is when the adapter received/emitted the record (UTC).
	ReceivedAt time.Time
	// Simulated is true for any data that did not come from a live vendor
	// system (CSV replay, mock feed, SITL replay). The demo must label this
	// data as simulated (NFR-08, R-02, R-09), so it travels with the record.
	Simulated bool
	// Payload holds the vendor-native fields. Keys and values are untouched
	// apart from flattening nested objects (see rawutil.Flatten).
	Payload map[string]any
}

// HealthState is the coarse connection state reported by an adapter.
type HealthState string

const (
	HealthUnknown  HealthState = "unknown"  // never connected
	HealthHealthy  HealthState = "healthy"  // connected, data flowing
	HealthDegraded HealthState = "degraded" // partial failures or slow vendor
	HealthDown     HealthState = "down"     // cannot reach the vendor
)

// Health is what the connector manager records (state + last-contact time).
type Health struct {
	State       HealthState
	LastContact time.Time // last time the vendor responded / sent data
	LastError   string
}

// ErrPermanent marks an error that retrying will not fix (bad credentials,
// missing configuration). The connector manager should stop backing off
// and surface it. Adapters wrap it: fmt.Errorf("...: %w", vendor.ErrPermanent).
var ErrPermanent = errors.New("permanent adapter error")

// VendorAdapter is the contract between the ingestion front end and a vendor.
//
// Lifecycle (driven by the connector manager):
//
//	Connect  -> Subscribe (blocks, emitting records) -> Close
//
// Subscribe returns nil when the source is finished normally (for example a
// CSV file that has been fully replayed), and a non-nil error when the
// connection failed and the manager should apply exponential backoff and
// reconnect. It must return promptly when ctx is cancelled.
type VendorAdapter interface {
	Name() string
	Connect(ctx context.Context) error
	Subscribe(ctx context.Context, out chan<- RawRecord) error
	Health() Health
	Close() error
}
