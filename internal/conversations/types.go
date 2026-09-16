package conversations

import "time"

type Conversation struct {
	ID                     string     `json:"id"`
	ProviderConversationID string     `json:"provider_conversation_id"`
	ContactID              string     `json:"contact_id,omitempty"`
	Type                   string     `json:"type"`
	Title                  string     `json:"title,omitempty"`
	LastMessageAt          *time.Time `json:"last_message_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
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
