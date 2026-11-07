# Sprint 1 pull request review

Historical review of the original PR heads. The combined fixes and validation
are documented in [the integration guide](ingestion-test-infra.md). Findings
below describe the original submissions, not the integrated implementation.

Reviewed 2026-11-07 against the local Milestone 1 requirements PDF and main
`3e4a2c0e008cb948bd0377a185353efd4f87c731`. Request-changes reviews were submitted to all three PRs on 2026-11-07
following explicit user authorization. No approvals or merges were submitted.

| PR | Reviewed head | Scope | Recommendation |
| --- | --- | --- | --- |
| [6 — Hamza](https://github.com/omarzayedbeshir/drone-fleet-management/pull/6) | `be5456ff93a49158cedac1e3150ec9e566570411` | DJI/CSV sources, mock feed, credentials | Changes required: integrate adapter contract and CI |
| [7 — Ziyad](https://github.com/omarzayedbeshir/drone-fleet-management/pull/7) | `c3bb838f602bfe854524d703a9fed9fe1a27e41a` | Batch persistence, temporal counter validation, battery rating | Changes required: restore buildable package layout and complete integration |
| [8 — Ali](https://github.com/omarzayedbeshir/drone-fleet-management/pull/8) | `da3fa9028dc310f8ac525d569ba26f0b89ab8b92` | Schema, privacy, buffer, normalization | Changes required: build, privacy, normalization, and contract fixes |

These contributions cover different pipeline stages; they are not competing
implementations from which to select a single winner. None currently delivers
an integrated pipeline with the other two or the running backend.

## Findings, ordered by severity

### P1 — PR 8: privacy filter lets the provided mock camera frame through

`filter.go:64–80` blocks image/photo/video/payload key substrings but does not
block `camera_frame_b64`, which PR 6's `internal/mockfeed/generator.go:135`
actually emits. Passing this field to `Filter.Process` preserves its secret
value, allowing imagery into the buffer. This violates SOC-03 and UC-12b's
requirement to remove imagery immediately after adapter output.

Reproduced in an isolated copy of the documented package layout. Add the real
mock record to the privacy tests and use an explicit permitted telemetry field
contract, or otherwise cover every supported imagery representation before
buffering. Also test arrays of objects: PR 6's `rawutil.Flatten` stringifies
array elements before this filter can inspect their nested keys.

### P1 — PRs 7 and 8: submitted source cannot build or run its new tests

Both PRs put unrelated Go packages directly in the repository root and provide
no root `go.mod`. `go test ./...` there fails with “directory prefix . does not
contain main module or its selected dependencies.” Package inspection also
confirms `ardupilot` and `telemetry` coexist in PR 7, and `buffer`, `privacy`,
`normalize`, `pipeline`, `telemetry`, and an external test package coexist in
PR 8. PR 8 imports an absent `aerosync/telemetry-pipeline` module.

Move source and tests into proper packages under the existing backend module
and use its module import paths. PR 7 additionally references undeclared
`MaxBatch` and `BatchItem`; moving files alone is insufficient. Its new
`PostgresStore.Ingest` duplicates the existing method in
`backend/internal/telemetry/postgres.go`; reconcile the implementation rather
than simply copying the file into that package.

### P1 — PR 7: migrations and batch delivery never enter the application

`000007_telemetry_writer.sql` and `000008_telemetry_receipt_timestamp.sql` are
at the root, while the migration runner only reads `backend/migrations/`.
The new writer depends on `telemetry_storage_settings`, receipt columns, and
battery columns that therefore will not be installed. `deliverBatch` has no
production caller; the existing adapter and command still use single-record
delivery. Put migrations in the runner's directory, wire batch delivery and
the store interface, and test a fresh database and an upgrade from main.
FR-10, NFR-05, and the intended ingestion performance improvements are not
demonstrated by these disconnected files.

### P1 — PR 6: adapters implement a second, unused vendor contract

`internal/vendor/vendor.go:69–75` defines Connect/Subscribe/Close with raw
records, while the running backend's `internal/adapters/adapter.go` defines
Run/Health with normalized `telemetry.Sample` callbacks. The API and adapter
commands are unchanged and cannot consume the new sources. The new root
module makes the contribution testable in isolation but does not connect it
to the backend. Agree on one boundary, preserve organisation/drone binding,
and prove CSV replay reaches stored history and the existing dashboard.
This blocks FR-08 and the NFR-07 zero-core-change adapter objective.

### P1 — PR 8: normalization contracts disagree with both source and sink

`normalizer.go:40` does not recognize PR 6's `device_sn`; its altitude mapping
does not recognize `height`. A PR 6-shaped DJI record fails immediately with
“missing drone identifier.” The speed, battery, cumulative flight time,
cycle count, and fault aliases likewise omit the documented PR 6 names
`horizontal_speed`, `battery_capacity_percent`, `total_flight_time`,
`battery_loop_times`, and `fault_codes`.

Output also diverges from the running backend: it adds unsupported fields
such as `schema_version` and `drone_id`, uses `latitude_deg` and
`ground_speed_mps` instead of `latitude` and `speed_mps`, uses different
ArduPilot/CSV source identifiers, and lacks required operational status.
The backend's strict decoder rejects this representation. Choose one common
schema and test real source fixtures through normalization into the real
decoder/store. This is central to FR-09 and NFR-08.

### P1 — PR 8: a one-percent reading becomes a full battery

`normalizer.go:103–107` multiplies every reading in [0,1] by 100, even when
the explicitly named field is `battery_percent`. Reproduced input
`battery_percent: 1` produces 100. Conversion must depend on the field's
declared unit rather than magnitude. Add boundary cases for zero, one, and
fractional percentages. This is an FR-09 data correctness issue.

### P1 — All PRs: green CI omits the new implementation

The unchanged backend job runs only inside `backend/`; its module does not
traverse root source or PR 6's separate module. The database job also ignores
PR 7's root migrations. All four checks are green on the reviewed heads,
but that result does not validate the newly contributed code. Integrate into
the backend module or explicitly add checks for every additional module,
and require the new contract and persistence tests in CI (NFR-09).

## Requirements comparison

| Requirement | PR 6 | PR 7 | PR 8 | Remaining acceptance evidence |
| --- | --- | --- | --- | --- |
| FR-08 / UC-11: automatic vendor ingestion | Standalone DJI polling, CSV fallback, mock feed | — | — | Run a source through the actual ingestion path |
| FR-09 / NFR-08: common SI/UTC schema | Documents raw DJI fields | Uses existing backend Sample | Adds incompatible schema/normalizer | One source-to-storage contract for both sources |
| FR-10 / SOC-04: stored counters | Provides simulated cycle/flight values | Adds writer and battery rating | Optional normalized counters | Persist source counters, validate history, show downstream values |
| NFR-03: bounded outage handling | Returns errors for a future connector manager | Unwired retrying batch delivery | Bounded queue with backpressure | Integrated outage/recovery and capacity logging tests |
| NFR-05 / UC-12a, UC-12c: validate/log/store | Malformed-data injection support | Temporal checks and rejection persistence intent | Basic value validation | Database tests for mixed batches, duplicates, rollback, late events |
| NFR-07: shared adapter interface | New stub differs from backend | Uses existing backend adapter type | Adds separate stage interface | Shared documented extension path |
| NFR-09: PR tests and mock feed | Strong isolated test coverage; absent from CI | Submitted tests cannot run | Submitted tests cannot run | CI must execute all new code |
| SOC-03 / UC-12b: privacy before buffering | Emits imagery test fixtures | — | Filter misses provided camera key | Real mock fixtures and nested payload cases |

The PDF's Sprint 1 goals are the review scope. Later mission, maintenance UI,
AI, localization, and final load/coverage goals are not treated as missing
features of these particular contributions. DJI code explicitly documents
its endpoints and fields as assumptions, so live DJI support remains
unverified. The PDF permits a clearly labelled recorded DJI-format CSV
fallback if access is unavailable; no live DJI claim is necessary to use it.

## Validation performed

- Examined exact fetched PR heads in disposable archives under
  `/tmp/drone-pr-review/`; preserved the working checkout and local PDF.
- PR 6: `go test -race -count=1 -cover ./...` and `go vet ./...` passed in
  its root module. Coverage: CSV 97.9%, DJI 85.4%, source selection 95.7%,
  credentials 97.3%, mock feed 95.6%, raw utilities 87.2%. Command entry
  points and the adapter test helper reported 0%; these figures do not
  establish overall system coverage.
- PRs 7 and 8: root `go test ./...` failed before execution because no
  module exists. Package inspection confirms the additional layout issues.
- Reconstructed PR 8's documented directories and module in a separate
  temporary copy without changing implementation. Three added review tests
  reproduced the camera leak, 1-to-100 battery conversion, and rejection of
  PR 6's sample field shape. This reconstruction is diagnostic only and is
  not a claim that the submitted PR builds.
- Existing backend race tests passed in the PR 8 snapshot. Backend files
  are unchanged by all three PRs. Database/SITL integration tests requiring
  explicit environment configuration were not enabled for this review;
  no fresh-database or live-vendor acceptance run was performed.
- GitHub reports backend/frontend/database/OpenAPI checks passing for all
  three heads. No frontend changes exist in these diffs, so local frontend
  checks were not repeated.

## Recommended revision and integration order

1. Agree on the existing backend schema and adapter boundary, or make one
   deliberate coordinated migration with OpenAPI and consumer updates.
2. Integrate Hamza's mock feed and CSV source with CI. Use the recorded
   source as the shared fixture; treat assumed live DJI access separately.
3. Integrate Ali's filter/normalizer/buffer into real packages, fix the
   reproduced defects, and test against that fixture and backend decoder.
4. Integrate Ziyad's writer and migrations, resolve missing declarations
   and duplicate methods, and wire batching into the production command.
5. Require an end-to-end run covering stored/displayed telemetry, privacy
   before buffering, outage recovery, tenant binding, duplicate identity,
   and invalid-record logging before approval.
