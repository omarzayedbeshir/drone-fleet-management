package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HistoryEntry struct {
	ID             int64           `json:"id,string"`
	OrganisationID string          `json:"organisation_id"`
	ActorID        string          `json:"actor_id"`
	ResourceType   string          `json:"resource_type"`
	ResourceID     string          `json:"resource_id"`
	Action         Action          `json:"action"`
	Before         json.RawMessage `json:"before_data"`
	After          json.RawMessage `json:"after_data"`
	CreatedAt      time.Time       `json:"created_at"`
}

type HistoryFilter struct {
	OrganisationID string
	ResourceType   string
	BeforeID       *int64
	Limit          int
}

type HistoryReader interface {
	List(context.Context, HistoryFilter) ([]HistoryEntry, error)
}

type PostgresHistory struct{ Pool *pgxpool.Pool }

func (s PostgresHistory) List(ctx context.Context, filter HistoryFilter) ([]HistoryEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, organisation_id::text, actor_id::text,
		resource_type, resource_id::text, action, before_data, after_data, created_at
		FROM audit_events
		WHERE organisation_id = $1 AND ($2 = '' OR resource_type = $2)
			AND ($3::bigint IS NULL OR id < $3)
		ORDER BY id DESC LIMIT $4`, filter.OrganisationID, filter.ResourceType, filter.BeforeID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []HistoryEntry{}
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.ID, &entry.OrganisationID, &entry.ActorID, &entry.ResourceType,
			&entry.ResourceID, &entry.Action, &entry.Before, &entry.After, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entry.CreatedAt = entry.CreatedAt.UTC()
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func ValidResourceType(value string) bool { return resourcePattern.MatchString(value) }
