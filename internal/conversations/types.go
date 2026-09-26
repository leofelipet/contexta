package conversations

import "time"

type Conversation struct {
	ID                     string     `json:"id"`
	ProviderConversationID string     `json:"provider_conversation_id"`
	ContactID              string     `json:"contact_id,omitempty"`
	Type                   string     `json:"type"`
	Title                  string     `json:"title,omitempty"`
	LastMessageAt          *time.Time `json:"last_message_at,omitempty"`
	LastMessage            *Preview   `json:"last_message,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type Preview struct {
	ID        string    `json:"id"`
	Direction string    `json:"direction"`
	Type      string    `json:"type"`
	Text      string    `json:"text,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type ListParams struct {
	Query     string
	ContactID string
	From      *time.Time
	To        *time.Time
	Limit     int
	Cursor    string
}

type Page struct {
	Conversations []Conversation
	NextCursor    string
}

type StaleParams struct {
	Days   int
	Limit  int
	Cursor string
}

type StaleConversation struct {
	ID            string     `json:"id"`
	Type          string     `json:"type"`
	Title         string     `json:"title,omitempty"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	MessageCount  int64      `json:"message_count"`
	InactiveDays  int        `json:"inactive_days"`
	Blocked       bool       `json:"blocked"`
}

type StalePage struct {
	Conversations []StaleConversation
	NextCursor    string
	Days          int `json:"days"`
}

type DeleteResult struct {
	Deleted       bool  `json:"deleted"`
	MessageCount  int64 `json:"message_count"`
}
