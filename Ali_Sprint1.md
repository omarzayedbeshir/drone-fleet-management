# AeroSync telemetry pipeline — Ali's Milestone 2 contribution

This standalone Go module implements the common telemetry schema and the pipeline-middle components assigned to Ali:

- Versioned common telemetry schema (SI units, UTC timestamps)
- Shared pipeline-stage interface
- Privacy filter (runs before the ingress buffer)
- Bounded ingress buffer with context-aware backpressure
- Vendor-specific normalization (MAVLink/ArduPilot, DJI, and CSV/replay)
- Shared normalized-telemetry contract tests

## Important integration boundary

This module deliberately does not implement vendor adapters, validation/de-duplication, database persistence, or the connector manager. Adapters should emit `map[string]any` records (`telemetry.RawRecord`). The first operation after adapter output must be `privacy.Filter.Process`; only its filtered result should be sent to `buffer.Buffer`. A downstream consumer can then pass dequeued records to `normalize.Normalizer.Process`.

The common schema is version `1.0`. It is intentionally independent of any particular adapter implementation. The field names include units where applicable.

## Project layout

```text
telemetry/
  raw.go
  schema.go
  contract.go
pipeline/
  stage.go
privacy/
  filter.go
buffer/
  buffer.go
normalize/
  normalizer.go
contract/
  contract_test.go
```

## Run tests

```bash
go test ./...
go test -race ./...
```

## Minimal usage

```go
filter := privacy.NewFilter()
filtered, err := filter.Process(ctx, rawRecord) // MUST happen before buffering
if err != nil { return err }

ingress := buffer.New(256)
if err := ingress.Enqueue(ctx, filtered); err != nil { return err }

// In a downstream worker:
record, err := ingress.Dequeue(ctx)
if err != nil { return err }

normalizer := normalize.New()
normalized, err := normalizer.Process(ctx, record)
if err != nil { return err }

// normalized is telemetry.Telemetry and can be passed to the next stage.
```

## Raw input conventions

The normalizer accepts common aliases. Examples:

- MAVLink/ArduPilot: `sysid`, `time_usec`, `lat`, `lon`, `alt`, `relative_alt`, `groundspeed`, `heading`, `battery_remaining`
- DJI: `drone_sn`, `timestamp`, `latitude`, `longitude`, `altitude`, `speed`, `battery`
- CSV/replay: `drone_id`, `observed_at`, `latitude_deg`, `longitude_deg`, `altitude_m`, `ground_speed_mps`

MAVLink latitude/longitude integers in degrees × 1e7 and altitude in millimetres are converted automatically when those field names are used. DJI/CSV numeric values can use explicit unit suffixes (for example `altitude_ft`, `speed_kmh`, `speed_mph`, `flight_time_minutes`).

The normalizer rejects records missing a drone identifier, timestamp, or usable coordinates. The downstream validation/storage owner can apply additional business rules and de-duplication.
