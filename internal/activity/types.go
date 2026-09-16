package activity

import (
	"context"
	"encoding/json"
	"time"
)

type Event struct {
	ID         string          `json:"id"`
	Category   string          `json:"category"`
	Level      string          `json:"level"`
	Operation  string          `json:"operation"`
	Outcome    string          `json:"outcome"`
	EntityType string          `json:"entity_type,omitempty"`
	EntityID   string          `json:"entity_id,omitempty"`
	Metadata   json.RawMessage `json:"metadata"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type Record struct {
	Category   string
	Level      string
	Operation  string
	Outcome    string
	EntityType string
	EntityID   string
	Metadata   json.RawMessage
}

type ListParams struct {
	Category string
	Level    string
	From     *time.Time
	To       *time.Time
	Limit    int
	Cursor   string
}

type Page struct {
	Events     []Event
	NextCursor string
}

type Recorder interface {
	RecordActivity(context.Context, Record) error
}
