# Sprint 1 integration result

This change merges the exact contributor histories from PR #6 (Hamza), #7
(Ziyad), and #8 (Ali), then fixes their package and contract integration on top
of main `3e4a2c0`. There were no textual merge conflicts; incompatible source,
normalization, writer and CI assumptions required explicit reconciliation.

## Delivered against the Milestone 1 Sprint 1 scope

- FR-08 / UC-11: raw CSV and mock-tested DJI HTTP sources can feed the same
  running telemetry store as the existing ArduPilot adapter. CSV is explicitly
  simulated and is not counted as a supported live vendor.
- FR-09 / UC-12 / NFR-08: one published SI/UTC telemetry contract remains
  compatible with the REST API, JSON Schema and dashboard. Actual source field
  mappings replace the disconnected schema; explicit percentages retain units.
- SOC-03 / UC-12b: a scalar telemetry allowlist runs immediately after raw
  source output, before the bounded queue. Camera frames and arbitrary nested
  data are excluded; logs do not include raw rejected values.
- FR-10 / UC-13 / SOC-04: cumulative flight time, battery cycles and fault
  history are persisted. Battery ratings have an audited repository foundation;
  later maintenance alerts and retirement workflows are not claimed here.
- NFR-03 / UC-26: source/back-end retries, bounded buffering/backpressure and
  capacity logging; registry reads and filters mark stale/unseen drones unknown
  with a configurable threshold, preserving grounding and timestamps.
- NFR-05 / UC-12a / UC-12c: batch validation checks pending and stored temporal
  neighbours; receipts preserve exact retry identity after retention; invalid
  records are logged and storage writes remain atomic. Notifications publish
  only committed telemetry metadata.
- NFR-07 / NFR-09: all source, pipeline and writer packages live in the existing
  backend module, so backend CI now executes them. Extension boundaries and
  commands are documented in [the ingestion guide](ingestion-test-infra.md).

## Verification

Local checks on 2026-11-07:

- Full backend `go test -race -count=1 ./...` with `TEST_DATABASE_URL` set,
  plus `go vet ./...` and `go build ./...`.
- PostgreSQL integration: original authentication/registry/audit/telemetry
  suites plus recorded CSV pipeline, replay, mixed batches, migration upgrade,
  freshness, battery audit rollback and receipt safety after chunk retention.
- Development database upgrade from migrations 000001–000006 through 000008;
  second migration run skipped every applied file. Schema constraints passed.
  Fresh isolated schemas apply all application migrations in integration tests.
- Real isolated ArduPilot SITL on TCP 15761 to TimescaleDB: eight observations,
  three with GPS, no rejections, exact duplicate replay accepted, latest
  projection consistent with stored history.
- Frontend: six test files, 21 tests passed; TypeScript and production build
  passed. Recorded-source label is covered by a component test.
- OpenAPI: eight paths, 12 operations, 32 validated examples; routes, DTOs and
  common telemetry schema aligned.

Live DJI endpoints/protocols have not been verified against granted vendor
access. The generated DJI-format CSV/mock fallback is used as permitted by
R-02. No full-system 1000-drone load result, monthly uptime, production TLS/
credential store, off-server backup/restore, or later-sprint application features
are claimed by this integration. The dashboard uses authenticated polling;
PostgreSQL NOTIFY consumers must enforce tenant scope and resync history.

The local requirements PDF is intentionally excluded from version control.
Contributor commits remain in the merge ancestry rather than being squashed
away or directly merged to main in their original broken state.
