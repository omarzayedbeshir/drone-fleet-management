package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type captureTx struct {
	pgx.Tx // Any unintended Begin/Commit/Rollback call panics, failing the test.
	called bool
	ctx    context.Context
	args   []any
	err    error
}

func (tx *captureTx) Exec(ctx context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.called = true
	tx.ctx = ctx
	tx.args = args
	return pgconn.NewCommandTag("INSERT 0 1"), tx.err
}
func event() Event {
	return Event{OrganisationID: "a4052e23-8b0f-49b3-bb88-9009b27e7ddc", ActorID: "48e472cd-2b80-4698-8fb4-556c9e8e3e76",
		ResourceType: DroneResource, ResourceID: "8cc0cc3e-d347-4cff-bb0f-156d7bc865f6", Action: Created, After: map[string]string{"name": "Survey"}}
}
func TestRecordsOnSuppliedTransaction(t *testing.T) {
	tx := &captureTx{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var absent *struct{ Name string }
	e := event()
	e.Before = absent
	if err := (PostgresRecorder{}).Record(ctx, tx, e); err != nil {
		t.Fatal(err)
	}
	if !tx.called || tx.ctx != ctx || len(tx.args) != 7 || tx.args[2] != DroneResource || tx.args[4] != Created {
		t.Fatal("did not use the supplied transaction/event")
	}
	if tx.args[5].([]byte) != nil || string(tx.args[6].([]byte)) != `{"name":"Survey"}` {
		t.Fatal("incorrect snapshots")
	}
}
func TestRejectsInvalidEventsBeforeWriting(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Event)
	}{
		{"organisation", func(e *Event) { e.OrganisationID = "invalid" }},
		{"actor", func(e *Event) { e.ActorID = "" }},
		{"resource ID", func(e *Event) { e.ResourceID = "invalid" }},
		{"resource type", func(e *Event) { e.ResourceType = "Drone" }},
		{"action", func(e *Event) { e.Action = "created;DROP" }},
		{"create before", func(e *Event) { e.Before = map[string]string{} }},
		{"missing update before", func(e *Event) { e.Action = Updated }},
		{"delete after", func(e *Event) { e.Action = Deleted; e.Before = map[string]string{} }},
		{"empty custom event", func(e *Event) { e.Action = "scheduled"; e.After = nil }},
		{"array", func(e *Event) { e.After = []string{"invalid"} }},
		{"scalar", func(e *Event) { e.After = "invalid" }},
		{"unencodable", func(e *Event) { e.After = make(chan int) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := &captureTx{}
			e := event()
			test.change(&e)
			if err := (PostgresRecorder{}).Record(context.Background(), tx, e); err == nil || tx.called {
				t.Fatal("invalid event was recorded")
			}
		})
	}
	if err := (PostgresRecorder{}).Record(context.Background(), nil, event()); err == nil {
		t.Fatal("accepted missing transaction")
	}
}
func TestStorageFailureIsPropagated(t *testing.T) {
	cause := errors.New("audit storage failed")
	tx := &captureTx{err: cause}
	err := (PostgresRecorder{}).Record(context.Background(), tx, event())
	if !errors.Is(err, cause) {
		t.Fatal("storage failure was swallowed")
	}
}
