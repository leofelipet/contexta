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
