# ArduPilot MAVLink adapter — SITL integration

`internal/adapters.VendorAdapter` is the shared streaming/health contract. `internal/adapters/ardupilot` implements it with gomavlib v4 and the ArduPilotMega dialect. `cmd/ardupilot-adapter` binds one registered ArduPilot drone to one flight-controller system/component and calls the existing normalized ingestion store. The web API and this worker run independently; no human login password or new public webhook is needed.

## Local setup

Use a compiled ArduPilot SITL checkout. The helper does not clone/build ArduPilot or modify its checkout. Follow the official build instructions if no binary exists.

From the repository root, start a separate simulator:

```sh
./backend/scripts/run-sitl.sh /absolute/path/to/ardupilot
```

The helper runs ArduCopter with its built-in physics, instance 20, Cairo home coordinates, and MAVLink TCP port 15760. The default state directory is `backend/.local/sitl` (ignored by Git), preserving EEPROM/statistics and boot count across runs. `SITL_STATE_DIR` can select another state directory. No arming, takeoff, or movement command is sent by the adapter/helper. This is simulated telemetry, not physical-aircraft verification.

To generate changing positions after connecting the adapter, use the separate [SITL flight demo](../docs/SITL_DEMO.md#5-fly-the-simulation): `python3 backend/scripts/fly-sitl.py` from the repository root. It verifies the isolated simulator, uses its secondary control port, flies a short square pattern, and lands. Flight-control commands remain outside the telemetry adapter and web API.

Register a drone in the frontend with **ArduPilot** as the vendor and a serial such as `AP-SITL-001`. Use a fleet manager in the same organisation. Stop the API before applying pending migrations; then from `backend/`:

```sh
set -a
source .env
set +a
./scripts/migrate.sh

# Existing fleet manager email and the serial you registered:
go run ./cmd/ardupilot-adapter \
  --endpoint tcp://127.0.0.1:15760 \
  --actor-email manager@example.com \
  --drone-serial AP-SITL-001 \
  --max-samples 5
```

Omit `--max-samples` to run continuously. Restart `go run ./cmd/api` in another terminal to view registry projections. Refresh the fleet page or use its Refresh button to reload registry values. Select **Show map** for telemetry readings, GPS trails; telemetry refreshes automatically every five seconds while the map is open. See [the map guide](../frontend/MAP.md). Use your actual manager email/serial; the worker resolves the UUIDs and organisation itself. Alternatively provide `--actor-id` and `--drone-id` UUIDs. Exactly one actor selector and one drone selector are required. Actor role/active status and organisation membership are rechecked before each ingestion attempt. Existing telemetry migrations (through 000006) are required; this adapter adds no migration.

For an existing `sim_vehicle.py` session, use `--endpoint tcp://127.0.0.1:5760`. For a MAVProxy UDP relay, run its `--out=udp:127.0.0.1:14550` and use `--endpoint udp://127.0.0.1:14550`; the adapter listens on that port. Default target IDs are system 1/component 1; configure `--system-id` and `--component-id` if your flight controller differs. One adapter process per registered drone is the supported binding.

Only the selected system/component is read, and the first verified flight-controller channel is pinned until it closes. Other systems/components/channels cannot overwrite that stream's mapping cache. TCP reconnects automatically. After disconnect, cached heartbeat, sensor, GPS, battery, clock, and parameter freshness are cleared before new output. MAVLink CRC validation comes from gomavlib; malformed frames are counted without retaining raw payloads. This local SITL binding is not a public authenticated vendor service; MAVLink signing/device authentication for untrusted networks is not introduced here.

## Field mapping

| MAVLink input | Common telemetry |
| --- | --- |
| `GLOBAL_POSITION_INT.lat/lon` with fresh 3D `GPS_RAW_INT` fix | Degrees: integer / 10^7. Without a valid fix, coordinates are null. |
| `GLOBAL_POSITION_INT.alt` | MSL metres: millimetres / 1000. `relative_alt` is **not** used as MSL altitude. |
| `GLOBAL_POSITION_INT.vx/vy/vz` | 3D speed magnitude in m/s: centimetres/second / 100. |
| Primary (`id=0`) `BATTERY_STATUS.battery_remaining` | Percentage, with -1 represented as null. Fresh primary battery readings take precedence over ambiguous `SYS_STATUS` battery data. Other battery IDs are not silently mixed. |
| `SYS_STATUS` battery value | Fallback only when no fresh primary battery reading exists. |
| `SYS_STATUS` present/enabled/unhealthy sensor mask | Deterministic `SENSOR_UNHEALTHY_...` fault code. Unsupported/disabled sensors are not marked faulty. |
| ArduPilot heartbeat critical/emergency/flight-termination state | Grounded plus `AUTOPILOT_CRITICAL`. Other states are conservative: healthy disarmed standby/active is available; armed alone is unknown. |
| Armed heartbeat plus fresh extended in-air/takeoff/landing state | In mission. |
| `PARAM_VALUE: STAT_FLTTIME` | Reported lifetime flight seconds. Never uptime or elapsed armed time. |
| Database battery-cycle total | Preserved baseline; the SITL feed does not establish a drone-wide cumulative battery-cycle count. No cycles are fabricated or inferred from reconnects/charge changes. |
| Fresh synchronized `SYSTEM_TIME` + position boot milliseconds | UTC measurement time; otherwise UTC receive time. With a synchronized clock, old/delayed positions are dropped rather than relabelled as fresh. |

Statistics parameters `STAT_BOOTCNT`, `STAT_RESET`, and `STAT_FLTTIME` are explicitly requested every five seconds and must be known/fresh before output. Sensor status and a verified ArduPilot heartbeat must also be fresh. Default sampling is at most one normalized observation/second; `--interval` changes the minimum boot-time separation. Optional measurements are null rather than zero when unavailable. Fault codes are explicit and privacy-safe; arbitrary `STATUSTEXT`, image/video messages, payload blobs, and unrelated parameter values do not cross the normalized boundary.

The worker sends GCS heartbeats, `REQUEST_DATA_STREAM`, read-only parameter requests, and message-interval requests for system time/extended status. It does not arm/disarm, change flight mode, upload missions, set vehicle parameters, or actuate controls. Stream rates are telemetry-link settings only.

## Identity, counters, and failure handling

Event identity uses configured system/component IDs, the reported statistics-reset marker and boot count, and the position's boot-millisecond timestamp. It does not depend on the receiver clock. Repeated/reordered position frames within a stream are ignored; reported reboots reset sampling state and invalidate cached readings. A queued normalized record is immutable across retries, retaining its event ID and timestamp. A second record with the same identity but changed normalized content is rejected by the ingestion pipeline, rather than duplicated.

Keep SITL state for the same virtual drone. Wiping statistics/EEPROM is a new baseline, not an ordinary reconnect: a lower `STAT_FLTTIME` is rejected and needs explicit operator reconciliation or a newly registered simulation drone. Do not substitute uptime or add arbitrary offsets to bypass lifetime-history checks. Very long uninterrupted boot-clock rollover/statistics-reset reconciliation is not implemented as a historical import/replay service.

Reported flight time can lag because ArduPilot periodically persists statistics; this adapter does not invent finer-grained totals. Battery-cycle measurement and per-battery identity/lifecycle tracking remain future work. Armed/in-air status mapping is operational telemetry, not a mission assignment or a maintenance clearance. Existing grounded projections remain grounded under the ingestion store's safety rule.

The reader and ingestion worker are separate. The default buffer holds 32 normalized observations. Temporary ingestion/storage failures retry the same record with exponential delays (250 ms up to 10 s); malformed/domain-rejected records are counted and dropped, allowing later data to proceed. If the buffer fills, new observations are dropped and counted, preserving queued records. The buffer is in-memory and does not survive process exit. Emit callbacks must honor context cancellation. Shutdown cancels retries and closes the MAVLink node.

Health is logged every ten seconds and on shutdown: connected/ready state, last heartbeat/frame/accepted observation, accepted/rejected/drop counts, parse errors, and a sanitized diagnostic. Connected is based on fresh verified heartbeat; ready additionally requires recent successful ingestion. A missing statistics stream therefore cannot appear ready just because a TCP socket connected. No new health REST endpoint is exposed. Stale-status expiration in the registry remains a separate future workflow.

## Verification

Unit tests cover scaling, unknown sentinels, counters, conservative status/faults, required/fresh inputs, source-derived event identity, reboot handling, privacy filtering, retry identity, permanent failures, and bounded buffering. A binary TCP mock checks CRC rejection and reconnects. These run in CI with normal backend tests; live SITL tests are opt-in.

With the isolated simulator running:

```sh
cd backend
SITL_MAVLINK_ENDPOINT=tcp://127.0.0.1:15760 \
  go test -race ./internal/adapters/ardupilot -run TestSITLIntegration -count=1 -v

# .env exported as above; this creates/drops an isolated database schema:
SITL_MAVLINK_ENDPOINT=tcp://127.0.0.1:15760 TEST_DATABASE_URL="$DATABASE_URL" \
  go test -race ./internal/httpapi -run TestArduPilotSITLIntegration -count=1 -v
```

The first check requires three GPS-backed observations that validate against the common schema. The second verifies accepted history and receipts, latest drone projection consistency, and idempotent replay in real TimescaleDB. Tests never create sample drones in the application's existing tenant schema. Only one test/worker should consume a given SITL TCP serial endpoint at a time.

Primary references: [ArduPilot SITL setup](https://ardupilot.org/dev/docs/using-sitl-for-ardupilot-testing.html), [MAVLink common messages and units](https://mavlink.io/en/messages/common.html), [ArduPilot statistics implementation](https://github.com/ArduPilot/ardupilot/blob/master/libraries/AP_Stats/AP_Stats.cpp), and [gomavlib](https://github.com/bluenviron/gomavlib).

Local verification on 2026-10-29 used the existing compiled ArduCopter SITL binary: normalized GPS/MSL/speed/battery/status observations passed common-schema validation, three GPS-backed observations reached an isolated TimescaleDB schema, latest totals matched history, and replay was idempotent. No physical vehicle was connected.
