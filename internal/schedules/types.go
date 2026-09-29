package schedules

import "time"

// Schedule is a recurring task template. On every scheduler tick, enabled
// schedules whose next_run_at has passed create one task from the template.
type Schedule struct {
	ID             string     `json:"id"`
	Cron           string     `json:"cron"`
	Timezone       string     `json:"timezone"`
	Enabled        bool       `json:"enabled"`
	SkipIfOpen     bool       `json:"skip_if_open"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	Company        string     `json:"company,omitempty"`
	DueInMinutes   *int       `json:"due_in_minutes,omitempty"`
	ConversationID string     `json:"conversation_id,omitempty"`
	ContactID      string     `json:"contact_id,omitempty"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	LastTaskID     string     `json:"last_task_id,omitempty"`
	RunCount       int        `json:"run_count"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type CreateParams struct {
	Cron           string
	Timezone       string
	Enabled        *bool
	SkipIfOpen     bool
	Title          string
	Description    string
	Company        string
	DueInMinutes   *int
	ConversationID string
	ContactID      string
}

// UpdateParams uses pointers to distinguish omitted fields from clears.
// Empty string on ConversationID/ContactID clears the link; a zero
// DueInMinutes clears the due offset.
type UpdateParams struct {
	Cron           *string
	Timezone       *string
	Enabled        *bool
	SkipIfOpen     *bool
	Title          *string
	Description    *string
	Company        *string
	DueInMinutes   *int
	ConversationID *string
	ContactID      *string
}

type ListParams struct {
	Enabled *bool
	Query   string
	Limit   int
	Cursor  string
}

type Page struct {
	Schedules  []Schedule
	NextCursor string
}

// RunResult summarizes one scheduler tick.
type RunResult struct {
	Created []string // task IDs created
	Skipped []string // schedule IDs skipped because their last task is still open
	Failed  []string // schedule IDs disabled because their cron could not be evaluated
}
