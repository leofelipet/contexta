package trello

import (
	"time"

	"github.com/leofelipet/contexta/internal/tasks"
)

type Settings struct {
	Configured   bool              `json:"configured"`
	Enabled      bool              `json:"enabled"`
	BoardID      string            `json:"board_id,omitempty"`
	ListStatuses map[string]string `json:"list_statuses,omitempty"`
	WebhookID    string            `json:"webhook_id,omitempty"`
	CreatedAt    time.Time         `json:"created_at,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at,omitempty"`
}

type ConfigureParams struct {
	APIKey            *string
	ApplicationSecret *string
	Token             *string
	BoardID           *string
	ListStatuses      *map[string]string
	Enabled           *bool
}

type CardSync struct {
	CardID      string
	Title       string
	Description string
	Status      string
	DueAt       *time.Time
	EventAt     time.Time
}

type WebhookResult struct {
	Processed bool       `json:"processed"`
	Task      tasks.Task `json:"task,omitempty"`
}
