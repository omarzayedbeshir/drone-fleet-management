# AeroSync

Mixed-vendor drone fleet management. React + TypeScript + Vite, a Go HTTP API with session authentication, and TimescaleDB.

## Prerequisites

- Node.js 22.12+ and npm
- Go 1.26+
- Docker with Compose (for the database)

## Local development

Start the database and apply migrations from the repository root:

```sh
cp .env.example .env
docker compose up -d --wait database
./backend/scripts/migrate.sh
```

Start the API in one terminal:

```sh
cd backend
set -a
. ./.env.example
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

The API now requires `DATABASE_URL`, a reachable database, and applied migrations. `GET /api/v1/health` remains a public process-liveness check. The drone registry REST API is implemented; vendor telemetry integrations and mission workflows remain future work. See [the registry API contract](backend/DRONES.md). See [authentication setup](backend/AUTH.md) to provision the first administrator and accounts for each role.

## Structure

```text
frontend/src/             React application
backend/cmd/api/         API entry point and graceful shutdown
backend/internal/httpapi/ HTTP routing and handler tests
backend/migrations/     Versioned database migrations
backend/scripts/        Migration runner
backend/tests/          Database integration checks
compose.yaml            Local TimescaleDB service
.github/workflows/      Frontend and backend CI
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

To run the PostgreSQL-backed authentication, registry, and audit tests locally:

```sh
cd backend
TEST_DATABASE_URL='postgres://aerosync:aerosync_local@localhost:5432/aerosync?sslmode=disable' go test -race ./internal/httpapi -run Integration -count=1
```

Use a development or test database. Tests create an isolated schema, apply the application migrations, exercise HTTP handlers with the PostgreSQL store, then drop the schema. Without `TEST_DATABASE_URL`, regular Go tests skip these integration checks.

CI runs the application checks, authentication/registry/audit integration tests, and database migrations/integration checks on pushes and pull requests. Stop the database with `docker compose down`; its named volume persists.
