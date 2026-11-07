# Battery-cycle baselines

Open an aircraft's details in the drone registry to view its cycle total and
source. Fleet managers and maintenance technicians can select **Record cycle
baseline**, enter a verified cumulative drone total and a service-log or battery
record reference, then save. Pilots can view the reading.

Use a documented drone-wide cumulative count. A replacement battery's individual
pack count is not automatically the aircraft's lifetime count. Counts cannot be
lowered through this workflow. A zero can be documented for a verified new asset;
an existing zero marked **Unverified baseline** is not evidence of no usage.

The four source labels distinguish unverified existing totals, telemetry-provided
counts, simulated CSV counts and documented manual readings. ArduPilot/SITL echoes
the configured baseline; it does not measure lifetime battery cycles. Its echo
preserves the existing provenance. A newer non-ArduPilot reading that advances
the total updates provenance to its source. Live DJI access remains unverified.

Every manual reading commits atomically with an audit entry and an append-only
historical boundary. It changes neither grounding, flight time nor telemetry
freshness. Telemetry that contradicts recorded boundaries is rejected, including
late arrivals after a newer reading has replaced the visible provenance.

Saving checks both the total and baseline revision shown when editing began.
If another writer changed them, reload the reading and verify the proposed total.
Active managed connectors are fenced immediately and reloaded by reconciliation.
Completed or failed connectors still require an explicit restart. Restart a
standalone adapter manually after recording a new baseline so it loads the new
counter; it otherwise may submit values that are rejected as regressions.

The API is `GET` / `PUT /api/v1/drones/{id}/battery-cycles`; see
[OpenAPI](../backend/openapi.yaml) for the request and response contract. Database
migration `000011` adds provenance without inventing sources for existing counts.
Run `backend/scripts/migrate.sh` with `DATABASE_URL` loaded, then restart the API
before using the new controls.

## Acceptance checks

1. Open an aircraft as a fleet manager or maintenance technician. Record a known
   cumulative total with a reference and confirm the manual source and timestamp.
2. Confirm grounding and last telemetry time remain unchanged by the reading.
3. Try a lower count; it must be rejected. Save from two open views; the stale
   view must require a reload rather than overwriting the newer reading.
4. Confirm a pilot can view the source but cannot record a reading.
5. With an active managed ArduPilot connector, record a valid baseline and confirm
   ingestion resumes with that total while retaining manual provenance.

Use a disposable aircraft for acceptance checks. The production workflow retains
the audit and reading history; it has no erase or downward-correction action.
