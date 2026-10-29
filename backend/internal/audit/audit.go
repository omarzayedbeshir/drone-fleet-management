// Package audit records tenant-scoped domain changes in the caller's transaction.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Action string

const (
	Created Action = "created"
	Updated Action = "updated"
	Deleted Action = "deleted"
)

const DroneResource = "drone"

// Event contains caller-selected, JSON-object snapshots. Callers must omit
// credentials and other secrets; audit history persists after actor deletion.
type Event struct {
	OrganisationID string
	ActorID        string
	ResourceType   string
	ResourceID     string
	Action         Action
	Before         any
	After          any
}

// Recorder must use tx for the write. It must not begin a separate transaction,
// commit, roll back, or swallow failures. The caller owns the transaction.
type Recorder interface {
	Record(ctx context.Context, tx pgx.Tx, event Event) error
}

type PostgresRecorder struct{}

var _ Recorder = PostgresRecorder{}

var resourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var actionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)

func validID(id string) bool {
	var uuid pgtype.UUID
	return len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' && uuid.Scan(id) == nil && uuid.Valid
}
func snapshot(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("audit snapshot cannot be encoded as JSON")
	}
	// A typed nil pointer is also an absent snapshot, not JSON null in storage.
	if string(data) == "null" {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, errors.New("audit snapshots must be JSON objects")
	}
	return data, nil
}

func (PostgresRecorder) Record(ctx context.Context, tx pgx.Tx, event Event) error {
	if tx == nil {
		return errors.New("audit recording requires an existing transaction")
	}
	if !validID(event.OrganisationID) || !validID(event.ActorID) || !validID(event.ResourceID) {
		return errors.New("audit organisation, actor, and resource IDs must be UUIDs")
	}
	if !resourcePattern.MatchString(event.ResourceType) || !actionPattern.MatchString(string(event.Action)) {
		return errors.New("audit resource type and action must be lowercase identifiers")
	}
	before, err := snapshot(event.Before)
	if err != nil {
		return err
	}
	after, err := snapshot(event.After)
	if err != nil {
		return err
	}
	valid := before != nil || after != nil
	switch event.Action {
	case Created:
		valid = before == nil && after != nil
	case Updated:
		valid = before != nil && after != nil
	case Deleted:
		valid = before != nil && after == nil
	}
	if !valid {
		return errors.New("audit snapshots do not match the event action")
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events
  (organisation_id,actor_id,resource_type,resource_id,action,before_data,after_data)
  VALUES ($1,$2,$3,$4,$5,$6,$7)`, event.OrganisationID, event.ActorID, event.ResourceType, event.ResourceID, event.Action, before, after)
	if err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	return nil
}
