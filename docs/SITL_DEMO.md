# Register a drone and stream SITL telemetry

This walkthrough demonstrates Omar's implemented path: ArduCopter SITL → standalone ArduPilot adapter → normalized validation/store → TimescaleDB → registry read API. It does not exercise the team's planned connector manager and shared pipeline stages. All flight data in this walkthrough is simulated.

## 1. Start the database and apply migrations

From the repository root, follow the [README setup](../README.md#local-development). Keep existing environment files and the database volume:

```sh
docker compose up -d --wait database
./backend/scripts/migrate.sh
```

Use `sudo docker compose` if Docker requires it. The migration runner also invokes Docker; in that case use `sudo ./backend/scripts/migrate.sh` for the Compose path, or export the backend configuration and run the script with host `psql`. See [schema migration instructions](../backend/SCHEMA.md).

The root `.env` controls Compose. `backend/.env` controls Go processes only after it is exported. Match `DATABASE_URL` to the actual database credentials and published port. If system PostgreSQL already occupies port 5432, choose a free `POSTGRES_PORT` in the root `.env` (for example 5433), recreate the Compose service with the command above, and use that port in `backend/.env`. Changing a password in `.env` does not change an existing database volume's password.

## 2. Start the app and register a drone

In an API terminal:

```sh
cd backend
set -a
. ./.env
set +a
go run ./cmd/api
```

In a frontend terminal:

```sh
cd frontend
npm ci
npm run dev
```

Open `http://localhost:5173` and log in as a fleet manager. If you need an account, follow [account provisioning](../backend/AUTH.md#local-setup); use an existing account when available. In the registry, create a drone with vendor **ArduPilot** and a unique serial such as `AP-SITL-001`. Keep its lifetime flight and battery-cycle totals at their initial values for a fresh simulator.

The fleet manager and drone must belong to the same organisation. A system administrator cannot substitute for the fleet manager in this workflow. Registration saves the inventory record; there is no UI Connect button yet.

## 3. Start ArduCopter SITL

In a simulator terminal, from the repository root:

```sh
./backend/scripts/run-sitl.sh /absolute/path/to/ardupilot
```

Supply your existing compiled ArduPilot checkout. Omar's verified local checkout is `/home/omar/Programming/GSoC/forks/ardupilot`; this is a machine-specific example, not a repository dependency. The helper requires `build/sitl/bin/arducopter`, keeps simulation state under ignored `backend/.local/sitl`, and exposes TCP port 15760. From `backend/`, use `./scripts/run-sitl.sh` instead.

Keep the simulator running. The helper and adapter send no arming, takeoff, or movement commands. Preserve its state when reusing the same registered simulation drone; see [counter and identity limits](../backend/ARDUPILOT.md#identity-counters-and-failure-handling).

## 4. Connect the adapter

In another terminal:

```sh
cd backend
set -a
. ./.env
set +a
go run ./cmd/ardupilot-adapter \
  --endpoint tcp://127.0.0.1:15760 \
  --actor-email manager@example.com \
  --drone-serial AP-SITL-001
```

Replace the email and serial with your actual fleet manager and registered drone. The adapter resolves their UUIDs and organisation, then checks actor access before each ingestion attempt. Use one consumer per SITL TCP serial endpoint. Add `--max-samples 5` to stop after five accepted observations; omit it for continuous ingestion.

Wait for `ArduPilot telemetry accepted` logs. Health appears every ten seconds; a socket connection alone does not mean ingestion is ready. Initial readings may lack GPS until a fresh 3D fix arrives. Refresh the fleet page or apply its filters to reload the latest readings. The app does not currently push live telemetry updates or display a connector health panel.

SITL flight time can remain zero while the simulated vehicle stays on the ground. Battery-cycle totals are preserved from the database; they are not measured from this feed. A stored grounded status remains grounded under the ingestion safety rule.

## 5. Verify and stop

For repeatable verification, use the [live SITL test commands](../backend/ARDUPILOT.md#verification). Stop the running adapter first so the tests can consume the TCP endpoint. Database tests use private schemas rather than the UI drone; they check stored history, receipts, latest projection consistency, and duplicate replay.

Stop each Go process and SITL with Ctrl+C in its own terminal. `docker compose down` stops the database while preserving its named volume. Do not use `down -v` to troubleshoot connection problems.

## Troubleshooting

| Symptom | Check or action |
| --- | --- |
| `DATABASE_URL is required` | Export `backend/.env` in the same terminal before running Go. Go does not load it automatically. |
| Database connection refused | Check `docker compose ps`; start the database and match its published port to `DATABASE_URL`. |
| Database authentication failure | Check the credentials of the existing volume; editing environment files does not reset them. |
| `Request origin is not allowed` | Use the exact browser origin in `APP_ORIGIN`, including host and port. `localhost` and `127.0.0.1` differ. Restart the API after changing configuration. |
| Adapter binding fails | Check the serial/email, ArduPilot vendor, active fleet-manager role, shared organisation, and applied migrations. |
| Connected but not ready | Check simulator heartbeat, statistics parameters, fresh sensor status, and accepted-ingestion logs. Consult the adapter's health diagnostic. |
| No connection or heartbeat | Keep SITL running, use port 15760 for the helper, check system/component IDs, and stop other consumers of that serial endpoint. |
| Lifetime flight time below baseline | Preserve SITL state or use a newly registered simulation drone after a statistics reset; do not fabricate offsets. |
| Registry readings appear unchanged | Confirm accepted logs, then refresh the page. Stationary SITL values may stay nearly constant. |

For transport options, exact field mappings, buffering/retry behavior, and supported limits, see [the adapter reference](../backend/ARDUPILOT.md).
