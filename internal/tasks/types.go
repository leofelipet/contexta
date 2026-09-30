package tasks

import "time"

const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// IsClosed reports whether a status marks the task as finished.
func IsClosed(status string) bool {
	return status == StatusDone || status == StatusCancelled
}

func ValidStatus(value string) bool {
	switch value {
	case StatusPending, StatusInProgress, StatusBlocked, StatusDone, StatusCancelled:
		return true
	default:
		return false
	}
}

type MemoryRef struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Source string `json:"source,omitempty"`
}

type Task struct {
	ID                string      `json:"id"`
	Title             string      `json:"title"`
	Description       string      `json:"description,omitempty"`
	CompanyID         string      `json:"company_id,omitempty"`
	CompanyName       string      `json:"company_name,omitempty"`
	Status            string      `json:"status"`
	DueAt             *time.Time  `json:"due_at,omitempty"`
	ConversationID    string      `json:"conversation_id,omitempty"`
	ContactID         string      `json:"contact_id,omitempty"`
	ConversationTitle string      `json:"conversation_title,omitempty"`
	ContactName       string      `json:"contact_name,omitempty"`
	ScheduleID        string      `json:"schedule_id,omitempty"`
	Memories          []MemoryRef `json:"memories,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

type CreateParams struct {
	Title          string
	Description    string
	CompanyID      string // empty inherits the linked contact's company
	Status         string
	DueAt          *time.Time
	ConversationID string
	ContactID      string
}

// UpdateParams uses pointers to distinguish omitted fields from clears.
// Empty string on DueAt/CompanyID/ConversationID/ContactID clears the value when the pointer is non-nil.
type UpdateParams struct {
	Title          *string
	Description    *string
	CompanyID      *string
	Status         *string
	DueAt          *string
	ConversationID *string
	ContactID      *string
	// DeleteMemories removes memories linked only to this task. Requires the
	// resulting status to be done or cancelled.
	DeleteMemories bool
}

// MemoryCleanup reports which linked memories were deleted along with a task
// and which were kept because other tasks still reference them.
type MemoryCleanup struct {
	DeletedMemoryIDs []string `json:"deleted_memory_ids,omitempty"`
	KeptMemoryIDs    []string `json:"kept_memory_ids,omitempty"`
}

type DeleteResult struct {
	Deleted bool `json:"deleted"`
	MemoryCleanup
}

type ListParams struct {
	Status         string
	CompanyID      string
	Company        string // company name substring
	ContactID      string
	ConversationID string
	ScheduleID     string
	Query          string
	Overdue        bool
	OpenOnly       bool // when true and Status empty, exclude done/cancelled
	Limit          int
	Cursor         string
}

type Page struct {
	Tasks      []Task
	NextCursor string
}
