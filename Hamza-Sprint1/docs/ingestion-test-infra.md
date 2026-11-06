# Hamza's Sprint 1 workstream: other sources and test infrastructure

Covers FR-08, NFR-09 (CI and mock feed), UC-11, R-02, R-09. Stdlib only, Go 1.22.

## What is where

| Path | What it is |
|---|---|
| `.github/workflows/ci.yml` | CI on every PR: `lint` (gofmt, vet, build) and `test` (race detector, TimescaleDB service, coverage summary) |
| `internal/vendor/vendor.go` | **STUB of Seif's `VendorAdapter` contract.** Replace with Seif's real package; fix signatures if they differ |
| `internal/credentials/` | `Provider` interface (+ `EnvProvider`, `StaticProvider`). The Sprint 2 encrypted-at-rest provider (NFR-04) implements the same interface, so adapters do not change |
| `internal/mockfeed/` | Mock vendor: slow / down / malformed injection. In-process adapter (`Feed`) and HTTP server (`NewServer`) |
| `cmd/mockfeed/` | Runs the mock as a server; faults can be flipped live via `PUT /admin/faults` |
| `internal/adapters/csvreplay/` | CSV/replay adapter (fallback, demo and test source) |
| `internal/adapters/dji/` | DJI adapter (polling REST) |
| `internal/adapters/djisource/` | Picks live DJI vs simulated replay (R-02) |
| `internal/adapters/adaptertest/` | Reusable conformance suite any adapter can run (NFR-07/NFR-09) |
| `internal/rawutil/` | `Flatten`, CSV value parsing, timestamp parsing shared by all three sources |
| `testdata/dji_sample.csv` | 120-row **simulated** DJI-format sample (3 drones). Regenerate: `make sample` |

## Commands

    make test       # all tests, race detector
    make lint       # what CI's lint job runs
    make cover
    make mockfeed   # mock vendor on :8089, token dev-token

Demo outage handling while the system runs:

    curl -X PUT localhost:8089/admin/faults -d '{"down":true}'
    curl -X PUT localhost:8089/admin/faults -d '{"latency_ms":3000}'
    curl -X PUT localhost:8089/admin/faults -d '{"malformed_rate":0.3,"malformed_kinds":["wrong_type","out_of_range"]}'
    curl -X PUT localhost:8089/admin/faults -d '{}'     # all clear

Malformed kinds: `missing_field`, `wrong_type`, `out_of_range`, `bad_timestamp`,
`duplicate`, `empty`, `broken_json` (HTTP only). Each is something Ziad's
validator must reject/log or de-duplicate. `IncludeImagery` / `-imagery` adds
`camera_frame_b64` and `payload_*` fields that Ali's privacy filter must drop.

Docker Compose snippet for Omar's file:

    mockfeed:
      build: { context: ., dockerfile: Dockerfile.mockfeed }
      command: ["-token", "dev-token", "-drones", "5", "-imagery"]
      ports: ["8089:8089"]

## Raw record shape (agree this with Ali: it is the input to his normalizer)

Every source emits `vendor.RawRecord{Vendor, SourceDroneID, ReceivedAt, Simulated, Payload}`.
`Payload` is **flat** (nested objects joined with `_`; empty cells/lists omitted).

| Raw key | Example | Unit / note for the DJI mapping |
|---|---|---|
| `device_sn` | `SIM-DJI-A001` | vendor drone id |
| `timestamp` | `1792486800500` | **epoch milliseconds** (number) -> ISO 8601 UTC |
| `latitude`, `longitude` | `30.0445` | degrees |
| `height` | `50` | metres |
| `horizontal_speed`, `vertical_speed` | `5.5` | m/s |
| `attitude_head` | `33` | degrees |
| `battery_capacity_percent` | `94.8` | percent |
| `battery_voltage` | `22800` | **millivolts** -> volts |
| `battery_temperature` | `31.5` | degrees Celsius -> SI |
| `battery_loop_times` | `207` | battery cycle count (SOC-04) |
| `total_flight_time` | `248401` | seconds (flight hours = /3600) |
| `mode_code` | `5` | vendor flight mode |
| `fault_codes` | `0x0A01;0x0B02` | `;`-joined, absent when none |

`Simulated` is `true` for CSV replay and the mock, `false` for live DJI. It lives
on the record (not in `Payload`) so the privacy filter and normalizer cannot drop it.
It must be carried through to storage/dashboard so the demo can say "simulated" (NFR-08).

## Things you MUST verify (I could not)

1. **DJI endpoints and fields.** `dji.Config` defaults (`/devices`,
   `/devices/%s/telemetry`, `X-User-Token`, `{"code","message","data"}` envelope,
   poll-based) are modelled on DJI's Cloud API style, not copied from your granted
   access. Check them against what DJI actually gave you; adjust `Config` defaults
   or `Adapter.get`. If DJI delivers over MQTT/WebSocket instead of REST, only
   `Subscribe` and `get` change. Then update the field table above and
   `testdata/dji_sample.csv` so the fallback matches reality.
2. **Real field names/units** in the table above are plausible placeholders; confirm with one real response, then tell Ali.
3. **Contract stub.** Compile against Seif's real `VendorAdapter` once merged.
4. **Not run here:** the GitHub Actions workflow (YAML validated, commands run locally) and the Dockerfile.

## Behaviour contract worth knowing (for Seif's connector manager)

- `Subscribe` returns `nil` when the source is finished (CSV file done), an error on failure, and returns promptly on cancel.
- Errors wrapping `vendor.ErrPermanent` (bad credentials, bad config, unreadable CSV) should NOT be retried. Everything else is retryable with backoff. `ErrPermanent` is my addition to the contract: raise it with Seif.
- The DJI adapter tolerates `MaxConsecutiveFailures` (default 3) failed poll cycles, then returns an error so the manager backs off. Exponential backoff itself lives in the manager (NFR-03), not in adapters.
- Adapters never drop imagery or "fix" bad values; that is the privacy filter's and validator's job.

## Sprint 1 checklist

- [x] DJI developer access (done already)
- [ ] CI merged and set as required checks `lint` + `test` (Omar: branch protection)
- [ ] Mock feed merged **first** (Ali and Ziad are waiting on it)
- [x] CSV/replay adapter + simulated DJI sample
- [x] DJI adapter + credentials interface
- [ ] Confirm DJI endpoints/fields with real access, update sample if needed
- [ ] End-to-end run with Ali/Ziad/Seif; R-01 schema check against both vendors' samples
