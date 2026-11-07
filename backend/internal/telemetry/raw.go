package telemetry

// RawRecord is the vendor-specific record emitted by a VendorAdapter.
// Values are decoded JSON-like values (string, bool, numbers, arrays, maps).
type RawRecord map[string]any
