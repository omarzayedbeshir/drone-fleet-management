# OpenAPI draft — NFR-08

The canonical REST/JSON draft is [openapi.yaml](openapi.yaml), using OpenAPI 3.1.1. It documents the 12 implemented operations across health, authentication, drone registry, and normalized telemetry ingestion/history. It is a source artifact; this change adds no `/openapi` or Swagger UI route to the running server.

## What is documented

- Session-cookie authentication for all four human roles, session expiry/revocation, exact Origin requirements, and production HTTPS requirements.
- Application-role permissions through `x-allowed-roles`, with organisation identity always derived on the server. These are application roles, not OAuth scopes.
- Requests, successful/error responses, examples, pagination/filter bounds, request-size limits, cookie/Location/Retry-After headers, and nullability.
- Registry audit consistency and deletion conflicts, telemetry identity/retry rules, delayed samples, monotonic counters, grounding, and rejection logging.
- SI measurement units, MSL altitude semantics, UTC response timestamps, and external references to the existing [common telemetry JSON Schema](schemas/telemetry.schema.json). The stored response schema reuses its individual field definitions while adding `received_at`.

The schemas describe canonical values. The server also normalizes metadata strings and checks stateful rules. Password limits are UTF-8 **bytes**, not JSON Schema character counts; schema extensions document those limits. A syntactically valid example is not a guarantee of success against existing accounts, serial numbers, event IDs, or current session state.

The draft does not invent endpoints for unfinished features. CSV/PDF exports and live MAVLink/ArduPilot plus DJI integrations (or the explicitly simulated recorded DJI fallback through a CSV adapter) remain NFR-08 launch work. Accepting a normalized `source` code is not evidence that its live adapter or a CSV file parser exists. Mission, maintenance, and organisation/user administration APIs also remain outside this implemented contract.

## Using the draft

Open the YAML in an OpenAPI 3.1-compatible viewer/editor, preserving `schemas/telemetry.schema.json` beside it. The relative references are intentional; uploading just the YAML to a remote viewer can leave them unresolved. Use tooling that supports local multi-file descriptions.

The development server choices are `http://localhost:5173` (Vite's same-origin API proxy) and `http://127.0.0.1:8080` (direct API for non-browser tooling). Adjust for your deployment; production uses HTTPS. Mutations still require the exact configured `APP_ORIGIN`, even from a non-browser client. Browsers generate Origin/cookie headers themselves; a viewer on another origin cannot override this protection and CORS is not enabled. No security changes were made to enable interactive documentation.

A typical client first logs in, retains the `aerosync_session` cookie, then uses it for requests. Cookies are HttpOnly and scoped to `/api`; clients should not copy them into localStorage or construct role/organisation claims.

## Validation

From the repository root, create a documentation-only environment:

```sh
python3 -m venv /tmp/aerosync-openapi-venv
/tmp/aerosync-openapi-venv/bin/pip install -r backend/requirements-openapi.txt
/tmp/aerosync-openapi-venv/bin/python backend/scripts/validate_openapi.py
```

The pinned direct dependencies are only needed for documentation checks, not the Go server. Once installed, validation needs no database, API server, or network access. CI runs the same check.

The validator checks OpenAPI structure, resolves local references, validates every documented request/response/parameter/header example, compares explicitly registered Go routes to documented operations, and compares public user/drone/telemetry DTO fields to schema fields. It also checks operation ID uniqueness, required Origin parameters on mutations, and bodyless 204 responses. Existing Go integration tests remain responsible for actual runtime authorization, stateful validation, transactions, and error behavior; this source-alignment check is not an exhaustive runtime conformance test.

When adding/changing an endpoint, update the draft and relevant examples alongside its implementation. Keep telemetry shape/units in the shared JSON Schema and reference it from the OpenAPI description. Planned endpoints should remain in requirement/backlog notes until implemented rather than appearing as callable operations.

Reference: [OpenAPI 3.1.1 specification](https://spec.openapis.org/oas/v3.1.1.html).
