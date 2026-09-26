package memories

import "time"

const (
	SourceNote    = "note"
	SourceMessage = "message"

	EmbeddingPending = "pending"
	EmbeddingReady   = "ready"
	EmbeddingFailed  = "failed"

	EmbeddingDimensions = 1536
	MaxContentRunes     = 12000
)

func ValidSource(value string) bool {
	switch value {
	case SourceNote, SourceMessage:
		return true
	default:
		return false
	}
}

type Memory struct {
	ID                string    `json:"id"`
	Title             string    `json:"title,omitempty"`
	Content           string    `json:"content"`
	Source            string    `json:"source"`
	MessageID         string    `json:"message_id,omitempty"`
	ConversationID    string    `json:"conversation_id,omitempty"`
	ContactID         string    `json:"contact_id,omitempty"`
	ConversationTitle string    `json:"conversation_title,omitempty"`
	ContactName       string    `json:"contact_name,omitempty"`
	EmbeddingStatus   string    `json:"embedding_status"`
	EmbeddingModel    string    `json:"embedding_model,omitempty"`
	EmbeddingError    string    `json:"embedding_error,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type CreateParams struct {
	Title          string
	Content        string
	Source         string
	MessageID      string
	ConversationID string
	ContactID      string
}

type UpdateParams struct {
	Title          *string
	Content        *string
	ConversationID *string
	ContactID      *string
}

type ListParams struct {
	ConversationID string
	ContactID      string
	Source         string
	Query          string
	Limit          int
	Cursor         string
}

type Page struct {
	Memories   []Memory
	NextCursor string
}

type SearchParams struct {
	Query          string
	ConversationID string
	ContactID      string
	Limit          int
}

type SearchHit struct {
	Memory Memory  `json:"memory"`
	Score  float64 `json:"score"`
}

type SearchResult struct {
	Hits []SearchHit `json:"hits"`
}
