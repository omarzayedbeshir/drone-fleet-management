# AeroSync

Mixed-vendor drone fleet management. React + TypeScript + Vite, a Go HTTP API with session authentication, and TimescaleDB.

## Prerequisites

- Node.js 22.12+ and npm
- Go 1.26+
- Docker with Compose (for the database)

## Local development

Start the database and apply migrations from the repository root:

```sh
# First setup only; keep an existing .env and database volume.
test -f .env || cp .env.example .env
docker compose up -d --wait database
./backend/scripts/migrate.sh
```

Start the API in one terminal:

```sh
cd backend
# First setup only; edit DATABASE_URL to match the root .env/Compose port.
test -f .env || cp .env.example .env
set -a
. ./.env
set +a
go run ./cmd/api
```

Start the frontend in another terminal:

```sh
cd frontend
npm ci
npm run dev
```

Open http://localhost:5173. Vite proxies `/api` requests to `http://127.0.0.1:8080`. Sign in using a provisioned account. If you open `http://127.0.0.1:5173` instead, change `APP_ORIGIN` to that exact origin.

The API now requires `DATABASE_URL`, a reachable database, and applied migrations. `GET /api/v1/health` remains a public process-liveness check. The drone registry REST API is implemented; normalized telemetry ingestion/history is also available. An ArduPilot MAVLink adapter is available and verified against SITL; DJI integration and mission workflows remain future work. See [ArduPilot/SITL setup](backend/ARDUPILOT.md). See [telemetry setup and schema](backend/TELEMETRY.md). See [the registry API contract](backend/DRONES.md). See [authentication setup](backend/AUTH.md) to provision the first administrator and accounts for each role.

## Structure

```text
frontend/src/             React application
backend/cmd/api/         API entry point and graceful shutdown
backend/internal/httpapi/ HTTP routing and handler tests
backend/migrations/     Versioned database migrations
backend/scripts/        Migration runner
backend/tests/          Database integration checks
docs/                   Architecture, sprint handoff, review workflow and spike evidence
compose.yaml            Local TimescaleDB service
.github/workflows/      Backend, frontend, database and OpenAPI CI
```

`API_ADDR` overrides the API listen address. Export backend environment variables in your shell; Go does not automatically read `.env`. See `backend/.env.example` for local database and authentication settings. The example credentials match the default Compose configuration; adjust them if your `.env` differs. If the API port changes, set `API_PROXY_TARGET` when starting Vite (for example, `API_PROXY_TARGET=http://127.0.0.1:8081 npm run dev`). Production hosting must serve the frontend and proxy `/api` to the backend on the same origin.

Database credentials in `.env.example` are for local development. Compose binds the database to loopback. Run `./backend/scripts/migrate.sh` after pulling new migrations. It works on both fresh and existing volumes, including volumes initialized by the original scaffold. The runner applies pending SQL files in filename order in one transaction, records their SHA-256 checksums in `schema_migrations`, and serializes concurrent runs with a PostgreSQL advisory lock. Already applied files must not be edited; add a new migration instead. Migrations are forward-only: use a corrective migration or restore a backup to reverse changes.

By default the runner uses `psql` inside the Compose database container. For another database, install `psql` and run `DATABASE_URL='postgres://...' ./backend/scripts/migrate.sh`. The database user must be able to create the TimescaleDB extension and schema objects. The API connects to the database on startup and fails if it is unavailable.

See [the schema documentation](backend/SCHEMA.md) for table relationships, constraints, and the boundary between database integrity and application authorization.

## Checks

```sh
cd backend
go test -race ./...
go vet ./...
go build ./...
```

```sh
cd frontend
npm test
npm run build
```

Database integration checks (with the Compose database running):

```sh
./backend/scripts/migrate.sh
# A second run must skip all applied migrations.
./backend/scripts/migrate.sh
docker compose exec -T database sh -c 'exec psql -X -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < backend/tests/schema.sql
```

These checks use disposable fixtures inside a rolled-back transaction. Run them against a development or test database.

To run the PostgreSQL-backed authentication, registry, audit, and telemetry tests locally:

```sh
cd backend
set -a
. ./.env
set +a
TEST_DATABASE_URL="$DATABASE_URL" go test -race ./internal/httpapi -run Integration -count=1
```

Use a development or test database. Tests create an isolated schema, apply the application migrations, exercise HTTP handlers with the PostgreSQL store, then drop the schema. Without `TEST_DATABASE_URL`, regular Go tests skip these integration checks.

CI runs the application checks, authentication/registry/audit/telemetry integration tests, and database migrations/integration checks on pushes and pull requests. Stop the database with `docker compose down`; its named volume persists.

## OpenAPI draft

[backend/openapi.yaml](backend/openapi.yaml) documents the implemented REST/JSON API for NFR-08, including cookie authentication, roles, registry operations, and normalized telemetry. It references the common telemetry schema and clearly identifies remaining NFR-08 work. See [API documentation and validation instructions](backend/API.md). CI validates the specification, examples, route coverage, and DTO/schema field alignment.

## ArduPilot / SITL

The separate `ardupilot-adapter` process reads a configured MAVLink TCP endpoint or UDP listener and sends normalized observations through the existing ingestion store. It is bound to one ArduPilot drone and an active fleet manager in the same organisation. See [backend/ARDUPILOT.md](backend/ARDUPILOT.md) for simulator launch, binding by email/serial number, field mappings, supported counters, health, and test commands.

## Sprint 1 platform handoff

See [stack and single-node server decisions](docs/ARCHITECTURE.md), [the R-15 spike result](docs/spikes/R15.md), and [the review workflow](docs/REVIEWING.md). `main` requires a pull request with one approval and passing backend, frontend, database, and OpenAPI checks.

For a walkthrough from UI registration to simulated telemetry, use the [SITL demo and troubleshooting guide](docs/SITL_DEMO.md). Registering a drone does not start a connection: the adapter runs separately, and the fleet page requires a refresh to show new readings.
