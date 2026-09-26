package denylist

import "time"

const (
	TargetConversation = "conversation"
	TargetContact      = "contact"
)

type Entry struct {
	ID          string    `json:"id"`
	TargetType  string    `json:"target_type"`
	TargetID    string    `json:"target_id"`
	TargetLabel string    `json:"target_label,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type AddParams struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason,omitempty"`
}

type ListParams struct {
	Limit  int
	Cursor string
}

type Page struct {
	Entries    []Entry
	NextCursor string
}
