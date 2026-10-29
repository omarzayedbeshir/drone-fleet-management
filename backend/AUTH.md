# Authentication

All four roles sign in through the same endpoint and screen: system administrator, fleet manager, pilot, and maintenance technician. Identity, role, and organisation come from the database. Users do not choose their role or tenant at login.

## Local setup

Start TimescaleDB and run migrations as described in the root README. Export `DATABASE_URL`, `APP_ORIGIN`, and `COOKIE_SECURE` from `backend/.env.example` in your shell. These commands are run from `backend/`:

```sh
set -a
. ./.env.example
set +a

# First platform administrator (no organisation):
go run ./cmd/create-user --name 'Platform Admin' --email admin@example.com --role system_administrator

# New organisation and its first manager, created atomically:
go run ./cmd/create-user --name 'Fleet Manager' --email manager@example.com --role fleet_manager --new-organisation 'Example Fleet'

# Use the organisation UUID printed by the manager command:
go run ./cmd/create-user --name 'Pilot' --email pilot@example.com --role pilot --organisation-id ORGANISATION_UUID
go run ./cmd/create-user --name 'Technician' --email technician@example.com --role maintenance_technician --organisation-id ORGANISATION_UUID
```

The CLI prompts for a hidden password and confirmation in an interactive terminal. For automation it reads one line from stdin, so a secret manager can pipe a password without putting it in process arguments. There are no built-in credentials. Passwords must be 12–72 bytes and are hashed using bcrypt cost 12. Email addresses are normalized to lowercase. The CLI is an operator tool with direct database access; do not expose it as an unprotected HTTP endpoint. Run it only against the intended database.

Start `go run ./cmd/api`, then start the frontend with `npm run dev`. Use `http://localhost:5173` when `APP_ORIGIN=http://localhost:5173`.

## HTTP contract

| Method / path | Authentication | Result |
| --- | --- | --- |
| `POST /api/v1/auth/login` | Public; rate limited | JSON `{ "email": "...", "password": "..." }`; sets a session cookie and returns `{ "user": ... }`. |
| `GET /api/v1/auth/me` | Session cookie | Returns the current user's ID, organisation ID (null for administrators), name, email, and role. |
| `POST /api/v1/auth/logout` | Optional session cookie | Revokes the presented session, clears the cookie, returns 204. Idempotent for missing/expired sessions. |
| `POST /api/v1/auth/password` | Session cookie; rate limited | JSON `{ "current_password": "...", "new_password": "..." }`; changes the password, revokes all sessions, clears the cookie, returns 204. |
| `GET /api/v1/health` | Public | Process liveness only. |

POST requests must carry the exact configured `Origin` header. Login and password changes require `Content-Type: application/json`, have a 4 KiB body limit, and reject unknown fields. Invalid, unknown, and inactive login accounts return the same 401 error. Session expiration returns 401; role denial returns 403; unavailable storage returns 503. An incorrect current password on a password change returns 400 without ending the session. No password hashes or raw session tokens are returned in JSON.

## Session and request protection

- A 256-bit random opaque token is set in the host-only, HttpOnly, SameSite=Strict `aerosync_session` cookie scoped to `/api`. The database stores only its SHA-256 hash. The cookie is a browser-session cookie; server-side expiry is authoritative.
- Sessions expire after 30 minutes without an authenticated API request and after 24 hours in all cases. Merely moving the mouse or typing does not refresh a session. The starter UI does not poll in the background to keep idle sessions alive.
- Re-login rotates the browser's existing session. Password changes revoke all devices. Account deactivation, reactivation, role changes, organisation changes, and direct database password updates revoke all sessions through a trigger.
- Origin validation covers every current state-changing endpoint, including login/logout, preventing cross-origin form submissions and login CSRF. CORS is not enabled; host the frontend and API on one origin.
- Login/password attempts are limited to 10 per minute per socket IP per API process, with a bounded client map and at most four concurrent password operations. Rate limits return 429 with `Retry-After`. `X-Forwarded-For` is not trusted. Behind a reverse proxy, clients share the proxy's bucket; use a trusted proxy-aware/shared limiter before scaling or opening a broader deployment.
- The server's authentication middleware resolves active users on every request and supplies a server-derived user/organisation context. Its role allowlist is tested for all four roles. All current authenticated endpoints allow all valid roles; future fleet, maintenance, and administration endpoints must explicitly apply their allowed roles and tenant filters. Frontend workspace visibility is presentation, not an authorization boundary.

Expired sessions for dormant users may be removed periodically using:

```sql
DELETE FROM auth_sessions
WHERE expires_at <= now() OR last_seen_at <= now() - interval '30 minutes';
```

## Production configuration

`COOKIE_SECURE` defaults to true. Set `APP_ORIGIN` to the exact HTTPS origin, without a trailing slash. Use HTTPS at the reverse proxy, forward `/api` on the same origin, keep the Go listener private, and use an appropriately secured PostgreSQL connection. Non-secure cookies are accepted only for localhost origins during development; the localhost example explicitly sets `COOKIE_SECURE=false`. Do not use the example database credentials in production.

Public registration, email invitations, forgotten-password recovery, MFA, organisation/user administration screens, and audit logging for future administration endpoints remain separate work. All four roles can currently be provisioned, authenticate, restore a session, change their own password, and sign out.

## Troubleshooting database access

If account creation reports database authentication failure, confirm which PostgreSQL instance owns the configured port. From the repository root, run `docker compose ps` (or `sudo docker compose ps` if your user lacks Docker access). A local PostgreSQL installation may already occupy port 5432.

`DATABASE_URL` must contain the actual username, password, port, and database name of the intended instance. The values in `.env.example` are development examples. Docker's `POSTGRES_PASSWORD` initializes a new database volume; editing `.env` does not reset the password in an existing volume. Keep the API connection settings aligned with the database's existing credentials. Do not delete a database volume to resolve a credential mismatch.

Once the connection is correct, run the migration script from the repository root and retry `go run ./cmd/create-user` from `backend/`. The CLI's `DATABASE_URL` must be exported in the same terminal where it is run.
