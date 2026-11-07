# MVP stack and server decision

Recorded 2026-10-29. Owner: Omar. Scope: Sprint 1 platform, R-15, SOC-05.

## Decision

Keep Go, React/TypeScript, and TimescaleDB on one server for the MVP. The functional [R-15 spike](spikes/R15.md) passed; there is no evidence requiring a stack change. This records the current implementation and deployment design, not a deployed production environment.

| Component | Choice | Reason and boundary |
| --- | --- | --- |
| Backend | Go 1.26+, standard `net/http`, `pgx/v5` | One API process with context cancellation, a bounded database pool, typed interfaces, and transactions shared by registry/audit writes. The module pins dependencies. |
| Frontend | React + TypeScript; Vite for development/build | Existing fleet/authentication screens share the REST API. Production serves built static files; Vite is the development server. |
| Storage | TimescaleDB 2.26.0 on PostgreSQL 17, pinned in Compose | One database supports relational tenant/registry/audit data and time-series telemetry. It avoids a separate telemetry datastore. |
| Integration | API-supervised connectors and optional standalone diagnostic workers; gomavlib v4 | Saved tenant/drone profiles drive supervised CSV/DJI/MAVLink adapters. A shared privacy/normalization pipeline feeds transactional batch ingestion; a PostgreSQL leader lock prevents duplicate managed supervisors. |
| Development | Docker Compose database, Go and Node tools on the host | The checked-in Compose file starts the database only. API/frontend/worker launch commands remain explicit. |
| MVP hosting | One Linux server, one database instance | Co-locate static web serving/reverse proxy, API, workers, and database. No cluster or high availability. No cloud provider or paid service is required by this decision. |

## Single-node topology

```mermaid
flowchart LR
    browser[Browser] --> proxy[HTTPS static server and reverse proxy]
    subgraph host[Single MVP Linux server]
        proxy --> api[Go API]
        api --> db[(TimescaleDB)]
        api --> worker[Managed source workers]
        worker --> db
    end
    sitl[ArduPilot SITL] --> worker
```

The proxy serves the frontend and forwards `/api` to the API on the same origin. TLS terminates at the proxy, with secure cookies and an exact configured `APP_ORIGIN`. Keep API/database ports private; Compose already binds the database to loopback. Configure an internal MAVLink link for SITL; public unauthenticated flight-controller exposure is outside this setup.

Server sizing is undecided until the Sprint 2 workload test and measured database growth establish a baseline. This decision selects a topology, not a hardware capacity or hosting provider. Hosting, storage, and operational time still need a deployment budget.

## Consequences and operating limits

- A single host failure stops the service. The 99% uptime target is a design target, not a demonstrated availability result.
- A named Compose volume persists database files across container restarts. It is not an off-server backup or a tested disaster-recovery procedure.
- Production needs restricted database credentials, HTTPS, backups and restore verification. The Compose database owner is for local development.
- The ingestion store supports COPY batches, compression/retention policies, battery rating configuration, and committed new-telemetry notifications. See the ingestion and connector guides.
- The adapter's buffer is bounded and in memory. It does not establish crash-durable queuing; saved connector configurations and runtime health are persisted independently.
- Keep applied migrations immutable. Add a numbered migration for schema changes; the runner rejects checksum changes and serializes concurrent runs.

## Revisit triggers

Revisit sizing or process boundaries if the agreed 1,000-drone workload, history queries, or concurrent users miss the requirements. Revisit the single-server choice if measured availability or recovery needs require another host. Record measurements before changing the stack or claiming NFR-01/NFR-02 compliance.

Implementation references: [Compose](../compose.yaml), [schema](../backend/SCHEMA.md), [authentication](../backend/AUTH.md), [telemetry](../backend/TELEMETRY.md), [SITL integration](../backend/ARDUPILOT.md).

## Alignment with the submitted component UML

The first alignment refactor introduces `internal/fleet.Service` between registry
HTTP handlers and `drones.Store`. The service implements create, list, details,
update and removal use cases. It derives tenant scope from the authenticated
identity, enforces reader/manager permissions, normalizes and validates metadata,
and discovers the installed vendor catalog through the repository capability.
HTTP handlers retain request decoding, query parsing, session/origin protection
and response/error mapping. Repositories retain database freshness evaluation,
vendor/uniqueness constraints and atomic domain/audit transactions.

This boundary is tested independently of HTTP, including forbidden callers,
missing tenant identity, protected fields, normalized inputs, catalog errors and
repository failures. Existing HTTP/database tests exercise the complete path.
The refactor changes neither API contracts nor database schema.

The following UML differences remain explicit follow-up work:

- Raw-source and normalized streaming adapter contracts still coexist. Unifying
  them must preserve MAVLink clock/state handling, privacy before buffering,
  backpressure, cancellation and external-module protocol compatibility.
- ArduPilot normalizes inside its adapter rather than the generic raw pipeline.
- Validation, duplicate checks and writing share one transaction. Their logical
  responsibilities can be separated without breaking that transaction boundary.
- Freshness is evaluated on repository reads; it is not a background process
  that mutates stored status. The map polls current registry status separately
  from immutable historical reports.
- Dashboard updates use REST polling. Committed PostgreSQL notifications provide
  the event boundary for future consumers, not a current browser subscription.

The UML describes the full application, including later-sprint services. This
alignment work does not claim scheduling, maintenance rules, notification or AI
services, live DJI compatibility, or encrypted credential storage as complete.
