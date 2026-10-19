package schedules

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ReminderTickInterval is how often due reminders are delivered; finer than
// TickInterval so one-off reminders fire close to their send_at.
const ReminderTickInterval = time.Minute

// MaxReminderAttempts bounds delivery retries before a reminder is marked failed.
const MaxReminderAttempts = 3

const (
	ReminderPending = "pending"
	ReminderSent    = "sent"
	ReminderFailed  = "failed"
)

// ErrInvalidNotification carries a user-facing reason for rejected reminder or
// schedule notification settings.
var ErrInvalidNotification = errors.New("invalid notification")

// Notification is one Pushover delivery requested by a schedule or reminder.
type Notification struct {
	Title    string
	Message  string
	Priority int
	Sound    string
}

// Notifier delivers a notification; the scheduler never retries a schedule
// notification but retries reminders up to MaxReminderAttempts.
type Notifier func(context.Context, Notification) error

// Reminder is a one-off Pushover notification delivered at SendAt.
type Reminder struct {
	ID        string     `json:"id"`
	Title     string     `json:"title,omitempty"`
	Message   string     `json:"message"`
	Priority  int        `json:"priority"`
	Sound     string     `json:"sound,omitempty"`
	SendAt    time.Time  `json:"send_at"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"last_error,omitempty"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type ReminderCreateParams struct {
	Title    string
	Message  string
	Priority *int
	Sound    string
	SendAt   time.Time
}

// ReminderUpdateParams only applies to pending reminders; nil fields are kept.
type ReminderUpdateParams struct {
	Title    *string
	Message  *string
	Priority *int
	Sound    *string
	SendAt   *time.Time
}

type ReminderListParams struct {
	Status string
	Limit  int
	Cursor string
}

type ReminderPage struct {
	Reminders  []Reminder
	NextCursor string
}

// ReminderRunResult summarizes one reminder delivery tick.
type ReminderRunResult struct {
	Sent     []string // reminder IDs delivered
	Retrying []string // reminder IDs that failed and will be retried
	Failed   []string // reminder IDs marked failed after MaxReminderAttempts
}

var localLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}

// ParseSendAt accepts RFC3339 (with offset) or a local date-time such as
// 2026-10-19T13:00 interpreted in timezone (DefaultTimezone when empty).
func ParseSendAt(value, timezone string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), nil
	}
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = DefaultTimezone
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: unknown timezone", ErrInvalidNotification)
	}
	for _, layout := range localLayouts {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: send_at must be RFC3339 or YYYY-MM-DDTHH:MM", ErrInvalidNotification)
}

// Pushover rejects longer fields.
const (
	maxNotificationTitle   = 250
	maxNotificationMessage = 1024
)

// ScheduleNotification builds the notification sent when a schedule creates a
// task: the schedule title as title and its description (or title) as message.
func ScheduleNotification(title, description string, priority int, sound string) Notification {
	message := description
	if strings.TrimSpace(message) == "" {
		message = title
	}
	return Notification{
		Title: truncateRunes(title, maxNotificationTitle), Message: truncateRunes(message, maxNotificationMessage),
		Priority: priority, Sound: sound,
	}
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
