package messages

import "time"

type Message struct {
	ID                string         `json:"id"`
	ProviderMessageID string         `json:"provider_message_id,omitempty"`
	ConversationID    string         `json:"conversation_id"`
	SenderContactID   string         `json:"sender_contact_id,omitempty"`
	SenderName        string         `json:"sender,omitempty"`
	Direction         string         `json:"direction"`
	Type              string         `json:"type"`
	Text              string         `json:"text,omitempty"`
	Status            string         `json:"status,omitempty"`
	Timestamp         time.Time      `json:"timestamp"`
	ReplyToMessageID  string         `json:"reply_to_message_id,omitempty"`
	Transcription     *Transcription `json:"transcription,omitempty"`
	AgentReadAt       *time.Time     `json:"agent_read_at,omitempty"`
}

type Transcription struct {
	Status        string     `json:"status"`
	Text          string     `json:"text,omitempty"`
	Language      string     `json:"language,omitempty"`
	Model         string     `json:"model,omitempty"`
	TranscribedAt *time.Time `json:"transcribed_at,omitempty"`
}

type SearchParams struct {
	Query          string
	From           *time.Time
	To             *time.Time
	ContactID      string
	ConversationID string
	Direction      string
	Type           string
	Limit          int
	Cursor         string
	ConsumerID     string
	ReadState      string
}

type UnreadParams struct {
	ConsumerID     string
	ConversationID string
	Direction      string
	Type           string
	Order          string
	Limit          int
	Cursor         string
}

type Page struct {
	Messages   []Message
	NextCursor string
}

type Around struct {
	Previous []Message `json:"previous"`
	Message  Message   `json:"message"`
	Next     []Message `json:"next"`
}
