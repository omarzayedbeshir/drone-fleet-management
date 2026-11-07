# Ali's Sprint 1 contribution

The privacy, ingress buffer and normalization implementation from PR #8 is now
integrated under `backend/internal/`, alongside Hamza's sources and Ziyad's
writer. See [the integration guide](docs/ingestion-test-infra.md) for the shared
contract, source mappings, commands and acceptance tests. There is one Go module
in `backend/`; use `make test` from the repository root.

The canonical schema is the existing `telemetry.Sample` and JSON Schema v1.
Privacy uses an allowlist before buffering, including removal of camera frames
and arbitrary nested payloads. Normalization preserves percentage units and
recognizes the actual DJI-format fixture fields. Tests exercise the combined
source-to-storage path rather than a separate standalone schema.
