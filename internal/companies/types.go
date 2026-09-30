package companies

import "time"

// Company groups tasks, recurring schedules, and WhatsApp contacts. Counts are
// computed on read.
type Company struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Notes         string    `json:"notes,omitempty"`
	OpenTaskCount int       `json:"open_task_count"`
	TaskCount     int       `json:"task_count"`
	ContactCount  int       `json:"contact_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type CreateParams struct {
	Name  string
	Notes string
}

// UpdateParams uses pointers to distinguish omitted fields from clears.
type UpdateParams struct {
	Name  *string
	Notes *string
}

type ListParams struct {
	Query  string // matches name, notes, or ID; "#12" matches only the exact ID
	Limit  int
	Cursor string
}

type Page struct {
	Companies  []Company
	NextCursor string
}
