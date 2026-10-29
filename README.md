# AeroSync

Mixed-vendor drone fleet management. Initial scaffold: React + TypeScript + Vite, a Go HTTP API, and a local TimescaleDB service.

## Prerequisites

- Node.js 22.12+ and npm
- Go 1.26+
- Docker with Compose (for the database)

## Local development

Start the optional database from the repository root:

```sh
cp .env.example .env
docker compose up -d --wait database
./backend/scripts/migrate.sh
```

Start the API in one terminal:

```sh
cd backend
go run ./cmd/api
```

Start the frontend in another terminal:

```sh
cd frontend
npm ci
npm run dev
```

Open http://localhost:5173. Vite proxies `/api` requests to `http://127.0.0.1:8080`. The starter screen checks `GET /api/v1/health` and shows the connection status.

The health endpoint checks process liveness only. The API does not connect to TimescaleDB yet; the frontend and API can run without Docker. No authentication, fleet data, vendor integrations, or mission workflows are implemented.

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

`API_ADDR` overrides the API listen address. Export backend environment variables in your shell; Go does not automatically read `.env`. See `backend/.env.example` for the planned database connection setting. If the API port changes, update the Vite proxy too. Production hosting must serve the frontend and proxy `/api` to the backend on the same origin.

Database credentials in `.env.example` are for local development. Compose binds the database to loopback. Run `./backend/scripts/migrate.sh` after pulling new migrations. It works on both fresh and existing volumes, including volumes initialized by the original scaffold. The runner applies pending SQL files in filename order in one transaction, records their SHA-256 checksums in `schema_migrations`, and serializes concurrent runs with a PostgreSQL advisory lock. Already applied files must not be edited; add a new migration instead. Migrations are forward-only: use a corrective migration or restore a backup to reverse changes.

By default the runner uses `psql` inside the Compose database container. For another database, install `psql` and run `DATABASE_URL='postgres://...' ./backend/scripts/migrate.sh`. The database user must be able to create the TimescaleDB extension and schema objects. The API still runs independently of the database.

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

CI runs the application checks and database migrations/integration checks on pushes and pull requests. Stop the database with `docker compose down`; its named volume persists.
