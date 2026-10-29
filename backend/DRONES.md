# Drone registry REST API

Base path: `/api/v1/drones`. Apply migrations through `000006_telemetry_ingestion.sql` and restart the Go API before using these endpoints. Authentication uses the existing session cookie. The frontend provides a registry workspace for fleet managers, pilots, and maintenance technicians. Managers can register, edit, and remove aircraft; pilots and technicians have read-only list/detail access.

## Access

| Role | List / retrieve | Register / edit / delete |
| --- | --- | --- |
| Fleet manager | Own organisation | Own organisation |
| Pilot | Own organisation | Denied |
| Maintenance technician | Own organisation | Denied |
| System administrator | Denied | Denied |

The registry is a tenant workspace. System administrators manage organisations separately and do not have a tenant registry context. Every registry SQL query uses the organisation from the authenticated session. There is no client-controlled tenant selector. Requests for a missing drone or a drone belonging to another organisation both return 404.

Mutations require `Origin` equal to the backend's `APP_ORIGIN` (for example `http://localhost:5173`). POST and PATCH require `Content-Type: application/json`, reject unknown/noneditable fields, and limit request bodies to 4 KiB. These are the same cookie/origin protections used for authentication.

## Endpoints

| Method / path | Success | Behavior |
| --- | --- | --- |
| `POST /api/v1/drones` | 201 | Register metadata. Sets `Location: /api/v1/drones/{id}`. |
| `GET /api/v1/drones` | 200 | List the authenticated organisation's fleet. |
| `GET /api/v1/drones/{id}` | 200 | Retrieve one drone. |
| `PATCH /api/v1/drones/{id}` | 200 | Update one or more metadata fields. |
| `DELETE /api/v1/drones/{id}` | 204 | Remove the registration; its audit history remains. |

POST requires all four metadata fields:

```json
{
  "name": "Survey 01",
  "vendor": "dji",
  "model": "Mavic 3 Enterprise",
  "serial_number": "DJI-0001"
}
```

Fields are trimmed, nonempty, at most 200 Unicode characters, and cannot contain control characters. Vendor codes are normalized to lowercase and limited to `dji` and `ardupilot`; CSV is an ingestion format, not a vendor. Serial numbers remain case-sensitive and unique within an organisation/vendor pair. Different vendors or organisations may use the same serial number.

PATCH accepts any nonempty subset of those fields. Omitted fields are preserved. Empty strings and explicit null values are invalid; identity, organisation, status, telemetry timestamp, and creation/update timestamps are not editable.

```json
{"name": "Survey 01 — North team"}
```

Create, retrieve, and update responses use this shape:

```json
{
  "drone": {
    "id": "8cc0cc3e-d347-4cff-bb0f-156d7bc865f6",
    "organisation_id": "a4052e23-8b0f-49b3-bb88-9009b27e7ddc",
    "name": "Survey 01",
    "vendor": "dji",
    "model": "Mavic 3 Enterprise",
    "serial_number": "DJI-0001",
    "status": "unknown",
    "flight_time_seconds": 0,
    "battery_cycles": 0,
    "fault_flags": [],
    "last_telemetry_at": null,
    "created_at": "2026-10-29T07:00:00Z",
    "updated_at": "2026-10-29T07:00:00Z"
  }
}
```

New drones always have `unknown` status and no telemetry timestamp. Registry edits preserve any existing operational status and telemetry timestamp. This API returns stored status; normalized ingestion updates current totals/status; freshness expiry and maintenance grounding transitions remain separate workflows.

## Listing

Supported query parameters are optional and may occur only once:

| Parameter | Default | Meaning |
| --- | --- | --- |
| `limit` | 20 | Page size, 1–100. |
| `offset` | 0 | Rows to skip, 0–1,000,000. |
| `vendor` | No filter | Exact `dji` or `ardupilot`. |
| `status` | No filter | `available`, `charging`, `in_mission`, `grounded`, or `unknown`. |
| `q` | No search | Trimmed literal substring search across name, model, and serial number; up to 200 characters. `%`, `_`, and backslash are literal characters. |

Example: `GET /api/v1/drones?vendor=dji&status=unknown&q=Survey&limit=20&offset=0`.

```json
{
  "drones": [],
  "pagination": {"limit": 20, "offset": 0, "has_more": false}
}
```

Results are ordered by creation timestamp descending, then UUID descending. Fetch the next page by increasing offset by limit while `has_more` is true. Offset pagination can shift when registrations are added or removed between requests. Unknown, repeated, or invalid query parameters return 400.

## Errors and audit history

Errors use `{ "error": "message" }`.

- 400: invalid fields, UUID, JSON, or query parameters.
- 401: missing, revoked, or expired session.
- 403: role denied or disallowed/missing request origin.
- 404: drone missing from the caller's organisation.
- 409: duplicate organisation/vendor/serial identity, or a conflicting related record.
- 415: POST/PATCH content type is not JSON.
- 503: storage or audit persistence is unavailable; no partial write is committed.

Each successful create/update/delete records its authenticated actor, organisation, drone ID, action, timestamp, and before/after snapshots in `audit_events` (`resource_type = 'drone'`). Failed requests create no audit event. Updates lock the drone while applying the patch so concurrent changes preserve omitted fields. Audit rows are append-only under ordinary SQL updates/deletes. They survive drone/user deletion and are removed only when their organisation is deleted. Registrations with accepted telemetry history cannot be deleted: the foreign-key conflict returns 409 and the deletion audit entry rolls back too.

## Verification

From `backend/`, with a disposable PostgreSQL database:

```sh
TEST_DATABASE_URL='postgres://USER:PASSWORD@HOST:PORT/DATABASE?sslmode=disable' \
  go test -race ./internal/httpapi -run Integration -count=1
```

Tests create and remove isolated schemas; they do not use or change existing drone/user records. They cover CRUD, all roles, tenant isolation, duplicate conflicts, pagination/search, read-only operational fields, audit snapshots/immutability, and transactional rollback when auditing fails. CI runs the same suites against the Compose TimescaleDB service.

## Frontend workspace

After sign-in, tenant users see the drone registry. Apply search/vendor/status filters with **Apply filters**; clear them to return to the full fleet. Use **View**, **Edit**, or **Remove** on a row; mutation controls are available only to managers. Registration resets filters and returns to the first page. Edits send only changed metadata fields, preserving omitted fields if another user updates the same drone. The UI keeps form values on validation/conflict errors, asks for confirmation before removal, and sends expired sessions back to sign-in. Status and telemetry timestamps are displayed without editable controls. There is no background polling that would extend idle sessions.

Frontend checks: `cd frontend && npm test` and `npm run build`. Component tests mock the HTTP boundary; PostgreSQL-backed server tests separately verify tenant/role enforcement. Browser checks use disposable accounts and synthetic aircraft.
