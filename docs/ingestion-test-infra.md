# Integrated source and pipeline guide

The Sprint 1 contributions from PRs #6, #7 and #8 now live in the single
`backend/go.mod` module and run in the existing backend/database CI jobs.
`make test` runs all backend race tests; `make lint` checks formatting, vet and
build; frontend and OpenAPI checks remain separate required CI jobs.

## Contracts and extension path

The public normalized contract is `backend/internal/telemetry.Sample`, version
1 in `backend/schemas/telemetry.schema.json` and `backend/openapi.yaml`. SI
measurements, UTC timestamps, integer cumulative counters and fixed source
identifiers (`ardupilot`, `dji`, `csv`) are shared with the API and dashboard.
The version is documented rather than adding an incompatible JSON field.
Unknown counters cause rejection; they never become invented zero totals.
Unknown operational mode becomes `unknown`, rather than guessed availability.
Explicit percentages remain percentages, including a 1% reading.

A normalized vendor adapter implements `internal/adapters.VendorAdapter`
(`Run`, `Health`). ArduPilot's MAVLink adapter already implements that contract.
Raw JSON/CSV sources implement the internal lifecycle in `internal/vendor`
(`Connect`, `Subscribe`, `Health`, `Close`). `internal/ingestion.Adapter` wraps
that lifecycle into the same normalized adapter boundary:

```
raw source -> scalar telemetry privacy allowlist -> bounded ingress queue
           -> vendor field normalization -> common decoder validation
           -> retryable normalized callback -> transactional telemetry writer
```

Add a raw source module implementing that lifecycle and configure the wrapper;
no changes to the common schema, writer, or dashboard are needed when the
source supplies the supported fields. Follow the CSV/DJI examples and
`internal/adapters/adaptertest` conformance suite. Add real source fixtures to
`internal/ingestion` tests. New vendor field mappings belong in normalization,
not the UI. A genuinely new measurement needs a coordinated schema change.

The wrapper binds exactly one vendor serial to an already registered drone.
Other serials in a multi-drone recording are ignored. Queue records are filtered
before buffering; arbitrary objects, media, payloads, and unknown fields cannot
cross that boundary. Malformed normalization is logged without raw values.
Store validation produces durable rejection records. Database retries retain
identical observations and event IDs. Source outages retry exponentially;
permanent configuration failures stop. Full queues apply backpressure and log
capacity pressure, with rate limiting. Buffers are in memory, not crash durable.

## Recorded DJI-format fallback

Apply migrations, register a DJI drone whose serial is `SIM-DJI-A001`, and use
an active fleet manager in the same organisation. From `backend/`, with your
existing environment exported:

```sh
go run ./cmd/vendor-adapter -source csv -csv testdata/dji_sample.csv \
  -drone-serial SIM-DJI-A001 -actor-email manager@example.test -speed 10
```

The committed fixture contains 40 observations each for three simulated drones.
It is generated data, not a recording from real DJI hardware. Its `height`
column represents MSL metres in this fixture. Stored `source=csv` and the map's
“Simulated / recorded telemetry” label identify the fallback. Original observation
timestamps are preserved; old recordings appear stale, never current. Replaying
an identical file deduplicates rather than inflating counters.

Retention defaults to 90 days, so much older recordings are rejected. To make
a new generated sample, choose a UTC start time sufficiently in the past that
all generated observations are within the five-minute future tolerance:

```sh
go run ./cmd/gensample -start 2026-11-07T08:00:00Z -out /tmp/dji-demo.csv
```

Use a fresh registered drone for a new simulated run whose generated cumulative
counters would contradict existing history. Do not reset real fleet counters
or rewrite old observation timestamps to disguise stale readings.

## Mock HTTP source and outage tests

From the repository root, `make mockfeed` starts the test server on port 8089.
It is a simulated configurable HTTP contract, not proof of live DJI compatibility.
Set `AEROSYNC_DJI_API_KEY=dev-token` in the operator shell. Register the matching
mock serial (default `MOCK-0001`), then from `backend/` run:

```sh
go run ./cmd/vendor-adapter -source dji -simulated -endpoint http://127.0.0.1:8089 \
  -drone-serial MOCK-0001 -actor-email manager@example.test
```

Fault controls:

```sh
curl -X PUT localhost:8089/admin/faults -d '{"down":true}'
curl -X PUT localhost:8089/admin/faults -d '{"latency_ms":3000}'
curl -X PUT localhost:8089/admin/faults -d '{"malformed_rate":0.3}'
curl -X PUT localhost:8089/admin/faults -d '{}'
```

Do not expose the fault-control server publicly. Credentials are redacted in
provider logs and supplied by environment variables; this is not an encrypted
credential-management service. Live DJI endpoint/protocol/field assumptions
must be confirmed against granted vendor access before selecting a live source.
No live DJI integration is claimed by the mock or CSV tests.

## Writer, freshness and battery configuration

`PostgresStore.IngestBatch` accepts 1–100 records for one bound drone. It locks
the drone, validates stored and pending temporal neighbours, COPY-inserts
accepted observations and rejection hashes, updates the newest projection once,
and transactionally publishes `new_telemetry` PostgreSQL notifications. Exact
retries survive sample retention through receipt hashes/timestamps. Consumers
must scope events to their organisation and resync history after reconnecting;
NOTIFY is a transient event feed, not durable delivery. The dashboard continues
using authenticated history polling. ArduPilot's production command uses batch
delivery with bounded collection latency and immutable retries.

Migration 000007 configures seven-day compression and 90-day sample retention.
An operator may change policies with `configure_telemetry_storage(retain,
compress_after)`; require `0 < compress_after < retain`. Receipts remain for
identity checks after sample retention. Migration 000008 backfills original
receipt timestamps for existing records. Applied migration files are immutable.

Registry GET/list and status filters calculate freshness using
`TELEMETRY_FRESHNESS` (default 30 seconds); stale/unseen drones show `unknown`,
while grounded aircraft remain grounded. Observation timestamps remain visible.
The calculation never rewrites audit history or stored telemetry.

Battery cycles are stored cumulatively. The `ConfigureBattery` repository method
validates rated-cycle/alert-fraction settings and writes their audit event in
the same transaction. These settings are a foundation for the later maintenance
rules workflow; this integration does not claim to deliver maintenance alerts.

## Acceptance evidence

`internal/ingestion` tests join the real CSV/HTTP mock sources to privacy,
normalization and retry handling. `internal/contract` tests include camera frame
removal, field units, 1% battery, small scaled coordinates and queue ownership.
PostgreSQL integration tests in `internal/httpapi` verify the committed CSV
fixture reaches history, repeats idempotently, and stays tenant scoped; mixed
batch outcomes, migrations, freshness and battery audit rollback are checked
alongside the existing authentication, telemetry and MAVLink transport tests.
Real SITL remains opt-in with `SITL_MAVLINK_ENDPOINT` and `TEST_DATABASE_URL`.
