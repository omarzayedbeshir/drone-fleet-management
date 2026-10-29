#!/usr/bin/env bash
# Forward-only migrations using the local Compose database by default.
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd -- "$script_dir/../.." && pwd)"

if [[ -n "${DATABASE_URL:-}" ]]; then
    psql_command=(psql "$DATABASE_URL")
else
    psql_command=(docker compose --project-directory "$repo_dir" exec -T database
        sh -c 'exec psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"')
fi

# Keep one session and transaction for the whole batch. The advisory lock also
# serializes concurrent runners before the migration ledger is created.
{
    cat <<'SQL'
\set ON_ERROR_STOP on
BEGIN;
SELECT pg_advisory_xact_lock(748291605);
CREATE TABLE IF NOT EXISTS schema_migrations (
    version text PRIMARY KEY,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);
SQL
    for migration in "$repo_dir"/backend/migrations/[0-9]*.sql; do
        version="$(basename -- "$migration" .sql)"
        if [[ ! "$version" =~ ^[0-9]+_[a-z0-9_]+$ ]]; then
            echo "Invalid migration filename: $version" >&2
            exit 1
        fi
        checksum="$(sha256sum "$migration")"
        checksum="${checksum%% *}"
        cat <<SQL
SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = '$version') AS applied,
       EXISTS (SELECT 1 FROM schema_migrations WHERE version = '$version' AND checksum <> '$checksum') AS changed
\gset
\if :changed
    \echo 'Previously applied migration changed: $version'
    -- Fail the transaction and leave all schema changes unapplied.
    DO \$\$ BEGIN RAISE EXCEPTION 'Migration checksum mismatch'; END \$\$;
\endif
\if :applied
    \echo 'Already applied: $version'
\else
    \echo 'Applying: $version'
SQL
        cat "$migration"
        printf '\n'
        echo "INSERT INTO schema_migrations (version, checksum) VALUES ('$version', '$checksum');"
        echo '\endif'
    done
    echo 'COMMIT;'
} | "${psql_command[@]}" -X --set=ON_ERROR_STOP=1
