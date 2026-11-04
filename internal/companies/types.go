package companies

import "time"

// Company groups tasks, schedules, WhatsApp contacts, conversations, and memories.
// Counts are computed on read.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Company struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Notes             string    `json:"notes,omitempty"`
	Rules             string    `json:"rules,omitempty"`
	Links             []Link    `json:"links"`
	OpenTaskCount     int       `json:"open_task_count"`
	TaskCount         int       `json:"task_count"`
	ContactCount      int       `json:"contact_count"`
	ConversationCount int       `json:"conversation_count"`
	MemoryCount       int       `json:"memory_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type CreateParams struct {
	Name  string
	Notes string
	Rules string
	Links []Link
}

// UpdateParams uses pointers to distinguish omitted fields from clears.
type UpdateParams struct {
	Name  *string
	Notes *string
	Rules *string
	Links *[]Link
}

type ListParams struct {
	Query  string // matches name, notes, rules, or ID; "#12" matches only the exact ID
	Limit  int
	Cursor string
}

type Page struct {
	Companies  []Company
	NextCursor string
}
