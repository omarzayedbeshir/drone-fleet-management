# Add an adapter module without changing the application

NFR-07 calls for a new adapter module with no changes to core application,
common schema or dashboard code. The host now supports a versioned executable
module interface and an installed vendor catalog. Native DJI/CSV/MAVLink adapters
remain available; their implementation need not change when a module is added.

## Installation

An operator installs a module directory containing an executable and profile
metadata. The module may be written in Go, Python or another language; the host
runs it directly without a shell. Select `source: external` and a stable vendor
code in the trusted profile catalog. Restarting the API loads that catalog and
installs its vendor metadata. The registry response includes vendor codes/labels,
so registration, filtering and display adapt without new UI branches.

A runnable synthetic starter is in `backend/examples/adapter-module/`. From
`backend/`, export the existing environment and start the API with:

```sh
CONNECTOR_PROFILES_FILE=examples/adapter-module/profiles.json go run ./cmd/api
```

Register an aircraft using **Example vendor (synthetic)**, give it a serial,
and enable the **Synthetic adapter module** profile in its details. The starter
emits one synthetic observation and completes. It is an extension example,
not an additional supported hardware vendor. Real vendor SDK/MQTT integrations
must obtain and normalize real source measurements rather than manufacture them.

The profile file replaces the catalog; include other desired profiles in the
same array. Relative executable paths resolve against the profile file's
directory. `executable`, `arguments`, `vendor`, `vendor_label`, `credential_ref`
and `queue_size` are operator settings; clients select only the profile ID.
Live profiles require an `organisation_id`, and other tenants cannot use them.
Removing a module does not delete vendor metadata or historical observations.

## Protocol: aerosync.telemetry.v1

At startup the host writes one JSON object to module stdin, then closes stdin:

```json
{"protocol":"aerosync.telemetry.v1","vendor":"example_vendor","serial":"DEMO-001","flight_time_seconds":0,"battery_cycles":0}
```

The counters are existing drone baselines, not newly measured values or authority
to fabricate increments. The module uses its SDK/source to select the bound
serial, converts units and altitude datum, drops private fields, and writes one
normalized telemetry JSON object per stdout line. Fields are exactly those in
`backend/schemas/telemetry.schema.json`; no extra version, credential, imagery,
raw payload or drone-ID fields are permitted. Unknown measurements remain null;
required cumulative counters must be known. The source code must equal the
profile vendor; simulated profiles are labelled CSV by the host before storage.

- ISO 8601 timestamps preserve observation time, not replay receive time.
- Stable event IDs must survive source retries. The host retains identical
  parsed observations during storage retries; a production module must also
  preserve source identity across reconnects/process restarts.
- A line is at most 16 KiB. Malformed, oversized, private/extra-field or wrong-
  source records are rejected and logged without raw contents. Overlong lines
  are drained with bounded memory before the next record is read.
- Exit 0 means the source completed. Nonzero exits are retried by the supervisor.
  Modules should terminate when the parent closes/cancels their process and
  should not daemonize or leave child processes running. Linux cancellation
  terminates the module process group, including SDK helpers.
- Only validated, normalized samples enter the bounded host queue. Capacity
  pressure is logged and applies backpressure. Temporary database errors retry
  immutable samples with exponential delays.

The child receives PATH and unbuffered Python output configuration, plus only
its explicitly referenced `AEROSYNC_<REF>_*` environment credential set. It does
not inherit DATABASE_URL or unrelated vendor accounts. stderr is discarded to
avoid persisting arbitrary SDK payloads/secrets; expose fixed health categories
through the host rather than relying on raw stderr for production monitoring.

Modules are trusted operator-installed software running as the API service user.
This process interface is not an operating-system sandbox or a tenant upload/
code-execution service. Never put module paths, arguments or raw secrets into
API requests. A production module needs its runtime/SDK installed by the operator.

## Acceptance evidence and scope

`TestNewVendorModuleIntegration` installs a previously unknown vendor via profile
metadata, registers its aircraft through the ordinary registry repository,
runs a separate module process under the managed connector, and verifies its
native source identity in the common telemetry store. No vendor-specific branch
in registry, writer, schema or frontend is added for the test vendor. A frontend
component test separately verifies a server-advertised vendor appears in the
registry and registration dropdown without a UI source edit.

Module transport tests cover bounded oversized-line rejection, extra-field
privacy rejection, exact storage retry identity, child cancellation, exit
failures and exclusion of DATABASE_URL from the child environment. Existing
native-adapter and database suites remain regression checks.

The executable acceptance run took approximately 1.4 seconds locally; this is
runtime evidence, not a measurement of SDK implementation effort. The PDF's
2-person-day integration target still needs a timed exercise with a real vendor
SDK and granted access. The starter/prototype provides a concrete guide for
that exercise. Dynamic tenant-driven vendor onboarding remains deferred.
