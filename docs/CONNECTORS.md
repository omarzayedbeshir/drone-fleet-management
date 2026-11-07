# Managed telemetry connections

Connector management implements the registry-driven lifecycle described by
NFR-03 / UC-25. The API process supervises enabled connections, restores them
after restart, and exposes tenant-scoped settings and health. The standalone
adapter commands remain useful for diagnostics; stop a standalone worker for
an aircraft before enabling its managed connector.

## Use from the application

1. Apply migrations and restart the API. Migration 000009 creates the connector
   settings/runtime table. Export the existing `backend/.env` before starting Go.
2. Register an aircraft, then choose **View details** in the registry.
3. Under **Telemetry connection**, choose a profile and **Enable connection**.
   Fleet managers can save/restart or disable connections; pilots and maintenance
   technicians can read status only. Settings belong to the authenticated tenant.
4. The supervisor starts the worker within its two-second reconciliation interval.
   The open details screen refreshes status every five seconds. Closing it stops
   polling. As with the map, authenticated polling keeps an open session active.

Without additional configuration, the API offers `demo-dji-csv`, the simulated
Nile Delta DJI-format recording at `backend/testdata/dji_sample.csv`. Register
`SIM-DJI-A001`, `SIM-DJI-B001` or `SIM-DJI-C001` to replay its matching rows.
The default path is relative to the backend working directory, as in the README.
Original observation timestamps and cumulative counters are preserved. Replays
remain idempotent. CSV reaches **Recording completed**, and does not loop or
restart merely because the API restarts. **Save and restart** explicitly starts
another replay. Continuous sources resume automatically after API restarts.

Connector state and health are separate from drone operational status. A
connection can be running while old readings leave the aircraft **Unknown**.
Health counters describe the current worker run, including accepted duplicates;
they are not lifetime flight/cycle counters. Supervisor snapshots older than ten
seconds are shown as unavailable, with their original update time retained.

## Operator profiles

`CONNECTOR_PROFILES_FILE` optionally replaces the built-in profile catalog with
a JSON array. Relative CSV paths resolve against the profile file's directory.
The file is trusted server configuration, not an upload/API input. An empty
array removes all selectable profiles; an existing obsolete profile can still
be disabled. To retry a failed/completed connection after changing the catalog,
save its settings again. Runtime changes to the file require an API restart.

Example `backend/connector-profiles.example.json` contains a simulated CSV and
SITL profile. Copy it to a local operator-owned file and customize it:

```sh
cd backend
set -a
. ./.env
set +a
CONNECTOR_PROFILES_FILE=connector-profiles.example.json go run ./cmd/api
```

Profiles accept `id`, `label`, `source` (`csv`, `ardupilot`, `dji`), `vendor`,
`simulated`, optional `organisation_id`, `endpoint`, `csv_path`, `credential_ref`,
`speed`, `queue_size` (1–4096; omitted/zero uses 32), and MAVLink `system_id` and
`component_id` (defaults 1). CSV sources currently read the DJI-format fallback.

Live profiles require an organisation UUID. They are visible and usable only
inside that organisation. This prevents a tenant from selecting another tenant's
vendor account. Simulated profiles may be shared; keep their data synthetic.
The source is still bound to the registered aircraft's vendor and serial.

DJI uses HTTPS for live profiles, with an environment credential reference such
as `credential_ref: org1_dji` resolving `AEROSYNC_ORG1_DJI_API_KEY`. Raw secrets
are never stored in connector rows, audit snapshots, API responses or profile
summaries. Live DJI endpoint/protocol compatibility remains unverified; a working
mock does not establish real vendor compatibility. A simulated HTTP mock profile
must explicitly set `simulated: true`; its stored telemetry is labelled CSV/
simulated. The existing environment provider is not an encrypted secret store.

Client requests select a profile ID only. They cannot supply arbitrary network
addresses, file paths or credentials. API responses do not reveal these operator
settings. Configure distinct profiles for distinct live drone endpoints/accounts.

## Supervision and write safety

One PostgreSQL session advisory lock per database schema elects the API leader.
It supervises all managed connections while reserving one pool connection, not
one connection per aircraft. Other API instances wait as standbys. Lost database
leadership cancels local workers before another reconciliation/re-election.

Each connector has a settings generation and a random worker ownership token.
Telemetry writes lock the owned aircraft and verify generation, ownership,
enabled state, serial/vendor identity and an active fleet-manager actor in the
same database transaction as observations/receipts/projection. Disabling or
changing settings fences the old worker before returning, even if its cancellation
has not yet been observed. Settings changes and their before/after audits commit
atomically. Runtime heartbeats are operational state, not repeated audit events.

Workers use bounded adapter queues and the existing immutable retry mechanisms.
Transient startup failures retry with exponential delays (250ms–10s). Permanent
configuration failures require settings to be saved again. Graceful shutdown
cancels workers; continuously enabled sources are restored on the next startup.
Process crashes lose in-memory queues; this does not provide durable messaging
or a historical vendor backfill service. Manual adapter processes do not take
managed-supervisor locks; avoid running both for the same source/aircraft.

## API and verification

- `GET /api/v1/drones/{id}/connector`: own-tenant fleet managers, pilots and
  technicians; returns `connector` (nullable) and eligible `profiles`.
- `PUT /api/v1/drones/{id}/connector`: own-tenant fleet managers; body
  `{"profile_id":"demo-dji-csv","enabled":true}`. Exact Origin protection,
  strict JSON and 4 KiB body limits apply. Saving advances generation and
  restarts an enabled worker; `enabled:false` disables it.

The OpenAPI contract documents the settings, states and health fields. CI runs
connector lifecycle tests against TimescaleDB alongside the HTTP integration
suite. Tests cover tenant and role access, profile privacy, configuration audit
rollback, CSV completion across restart, disabled-generation fencing, manager
revocation, competing supervisors and leader takeover. Frontend tests verify
profile selection, enable/disable, read-only status, polling cleanup and errors.

Local acceptance on 2026-11-07 also exercised the actual browser flow in a
disposable schema: register a DJI demo aircraft, enable its saved profile,
observe 40 accepted samples with no rejections, disable it, then replay again.
The aircraft's telemetry timestamp refreshed in the open dialog. The complete
backend race/database suite, vet/build, 26 frontend tests and production build
passed. OpenAPI validates 14 operations and 35 examples, including connector
DTO alignment. Migration 000009 applied successfully and a second run skipped
it; schema constraint checks passed.

## Adapter extension

Trusted `external` source modules can supply additional installed vendor codes
without new application branches. `vendor_label`, `executable` and `arguments`
are additional operator-only profile fields. See [the module guide](ADAPTER_MODULES.md).
