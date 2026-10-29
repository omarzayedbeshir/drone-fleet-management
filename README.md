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
docker compose up -d database
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
backend/migrations/     Database initialization SQL
compose.yaml            Local TimescaleDB service
.github/workflows/      Frontend and backend CI
```

`API_ADDR` overrides the API listen address. Export backend environment variables in your shell; Go does not automatically read `.env`. See `backend/.env.example` for the planned database connection setting. If the API port changes, update the Vite proxy too. Production hosting must serve the frontend and proxy `/api` to the backend on the same origin.

Database credentials in `.env.example` are for local development. Compose binds the database to loopback. Initialization SQL runs only when the database volume is first created; introduce a migration runner when schema development begins. Application tables and telemetry hypertables are intentionally left for that work.

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

CI runs these checks on pushes and pull requests. Stop the database with `docker compose down`; its named volume persists.
