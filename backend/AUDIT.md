# Shared audit recording

`internal/audit` provides the shared event model, transaction-aware `Recorder` interface, and PostgreSQL recorder. The drone registry is its first caller. This does not add automatic auditing to other services or expose an audit REST endpoint.

## Contract

```go
type Recorder interface {
    Record(ctx context.Context, tx pgx.Tx, event Event) error
}
```

`Event` carries `OrganisationID`, `ActorID`, `ResourceType`, `ResourceID`, `Action`, `Before`, and `After`. IDs must be UUIDs. Resource types and actions are lowercase identifiers with optional underscores/digits. The package provides `DroneResource` and the `Created`, `Updated`, and `Deleted` actions. Other resource types/actions can be supplied as workflows are implemented.

Snapshots must serialize as JSON objects. Nil (including typed nil pointers) means an absent snapshot, stored as SQL NULL. Created events require only the after snapshot; updated events require both; deleted events require only the before snapshot. Other actions require at least one snapshot. The database enforces the same shape constraints. The event timestamp is assigned by PostgreSQL.

Construct snapshots from explicit domain DTOs. Never include passwords, password hashes, session tokens, or vendor credentials in audit snapshots. The recorder intentionally has no dependency on the drone domain and cannot infer which fields in an arbitrary object's data are secret.

## Calling from a service

Inject `audit.PostgresRecorder{}` as an `audit.Recorder`. Begin a transaction in the service/repository, mutate the domain record, and call `Record` with that same transaction. Propagate every error and commit only after the recorder succeeds:

```go
tx, err := pool.Begin(ctx)
if err != nil {
    return err
}
defer tx.Rollback(ctx)

// Perform the domain mutation using tx, collecting before/after snapshots.
if err := recorder.Record(ctx, tx, audit.Event{
    OrganisationID: organisationID, // From server-authenticated context.
    ActorID:        actorID,
    ResourceType:   audit.DroneResource,
    ResourceID:     droneID,
    Action:         audit.Updated,
    Before:         before,
    After:          after,
}); err != nil {
    return err
}
return tx.Commit(ctx)
```

The production recorder requires an existing `pgx.Tx`, executes its insert on that transaction, and never begins/commits/rolls back a transaction itself. A custom implementation must honor the same contract. A missing registry recorder fails closed. The caller remains responsible for role checks and tenant scoping; event metadata is not an authorization mechanism.

## Storage and upgrade

Run `./backend/scripts/migrate.sh` before starting the updated API. Migration 000005 generalizes the existing table in place to `audit_events` with `resource_type` / `resource_id`. Original events become drone events without changing their IDs, snapshots, or timestamps. The old `drone_registry_events` table name is no longer available after migration; use the updated API and maintenance queries. Migration 000004 is not modified.

Audit rows survive actor and resource deletion. Normal UPDATE, DELETE, and TRUNCATE are rejected; deleting an organisation also removes its events, preserving the existing retention exception. Database owners can bypass triggers, so separate restricted application credentials remain necessary for production privilege enforcement.

## Verification

`go test ./internal/audit` checks validation, serialization, supplied-transaction use, and error propagation. The PostgreSQL integration suite additionally verifies an upgrade with pre-existing history, generic resource/action recording, caller rollback, injected recorder failures, and append-only protections. Run it against a development/test database using `TEST_DATABASE_URL` and `go test -race ./internal/httpapi -run Integration -count=1`; tests create and drop isolated schemas. CI runs these checks with the application integration tests.
