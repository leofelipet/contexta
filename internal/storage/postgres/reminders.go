package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/pagination"
	"github.com/leofelipet/contexta/internal/schedules"
)

// maxRemindersPerTick bounds one tick like maxSchedulesPerTick.
const maxRemindersPerTick = 100

// reminderPastTolerance lets a reminder be created for "now" even if the
// request arrives a little after the requested minute.
const reminderPastTolerance = 5 * time.Minute

const reminderSelectCols = `
	id::text, title, message, priority, sound, send_at, status, attempts, last_error, sent_at, created_at, updated_at`

func (s *Store) ListReminders(ctx context.Context, params schedules.ReminderListParams) (schedules.ReminderPage, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "reminders")
	if err != nil {
		return schedules.ReminderPage{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isTaskID(cursor.ID)) {
		return schedules.ReminderPage{}, pagination.ErrInvalidCursor
	}
	status := strings.TrimSpace(params.Status)
	if status != "" && status != schedules.ReminderPending && status != schedules.ReminderSent && status != schedules.ReminderFailed {
		return schedules.ReminderPage{}, fmt.Errorf("%w: %w: status must be pending, sent, or failed", ErrInvalidArgument, schedules.ErrInvalidNotification)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+reminderSelectCols+`
		FROM reminders
		WHERE ($1::timestamptz IS NULL OR (send_at, id) < ($1, $2::bigint))
		  AND ($3 = '' OR status = $3)
		ORDER BY send_at DESC, id DESC
		LIMIT $4`,
		nullableTime(cursor.Time), nullableTaskID(cursor.ID), status, limit+1,
	)
	if err != nil {
		return schedules.ReminderPage{}, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()

	items := make([]schedules.Reminder, 0, limit+1)
	for rows.Next() {
		reminder, err := scanReminder(rows)
		if err != nil {
			return schedules.ReminderPage{}, err
		}
		items = append(items, reminder)
	}
	if err := rows.Err(); err != nil {
		return schedules.ReminderPage{}, fmt.Errorf("iterate reminders: %w", err)
	}

	page := schedules.ReminderPage{Reminders: items[:min(limit, len(items))]}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "reminders", Time: last.SendAt, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetReminder(ctx context.Context, id string) (schedules.Reminder, error) {
	reminderID, err := parseTaskID(id)
	if err != nil {
		return schedules.Reminder{}, err
	}
	reminder, err := scanReminder(s.pool.QueryRow(ctx, `SELECT `+reminderSelectCols+` FROM reminders WHERE id = $1`, reminderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return schedules.Reminder{}, ErrNotFound
	}
	if err != nil {
		return schedules.Reminder{}, fmt.Errorf("get reminder: %w", err)
	}
	return reminder, nil
}

func (s *Store) CreateReminder(ctx context.Context, params schedules.ReminderCreateParams) (schedules.Reminder, error) {
	template := reminderTemplate{Title: params.Title, Message: params.Message, Sound: params.Sound, SendAt: params.SendAt}
	if params.Priority != nil {
		template.Priority = *params.Priority
	}
	if err := validateReminderTemplate(&template, time.Now()); err != nil {
		return schedules.Reminder{}, err
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO reminders (title, message, priority, sound, send_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text`,
		template.Title, template.Message, template.Priority, template.Sound, template.SendAt,
	).Scan(&id)
	if err != nil {
		return schedules.Reminder{}, fmt.Errorf("create reminder: %w", err)
	}
	return s.GetReminder(ctx, id)
}

// UpdateReminder edits a pending reminder; sent or failed reminders are
// history and return ErrConflict.
func (s *Store) UpdateReminder(ctx context.Context, id string, params schedules.ReminderUpdateParams) (schedules.Reminder, error) {
	reminderID, err := parseTaskID(id)
	if err != nil {
		return schedules.Reminder{}, err
	}
	current, err := s.GetReminder(ctx, id)
	if err != nil {
		return schedules.Reminder{}, err
	}
	if current.Status != schedules.ReminderPending {
		return schedules.Reminder{}, fmt.Errorf("%w: only pending reminders can be changed", ErrConflict)
	}
	template := reminderTemplate{
		Title: current.Title, Message: current.Message, Priority: current.Priority, Sound: current.Sound, SendAt: current.SendAt,
	}
	if params.Title != nil {
		template.Title = *params.Title
	}
	if params.Message != nil {
		template.Message = *params.Message
	}
	if params.Priority != nil {
		template.Priority = *params.Priority
	}
	if params.Sound != nil {
		template.Sound = *params.Sound
	}
	// An unchanged send_at may already be in the past while waiting for the next tick.
	checkFrom := time.Time{}
	if params.SendAt != nil {
		template.SendAt = *params.SendAt
		checkFrom = time.Now()
	}
	if err := validateReminderTemplate(&template, checkFrom); err != nil {
		return schedules.Reminder{}, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE reminders SET title = $2, message = $3, priority = $4, sound = $5, send_at = $6, updated_at = now()
		WHERE id = $1 AND status = 'pending'`,
		reminderID, template.Title, template.Message, template.Priority, template.Sound, template.SendAt,
	)
	if err != nil {
		return schedules.Reminder{}, fmt.Errorf("update reminder: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Delivered or deleted between the read and the update.
		return schedules.Reminder{}, fmt.Errorf("%w: only pending reminders can be changed", ErrConflict)
	}
	return s.GetReminder(ctx, id)
}

func (s *Store) DeleteReminder(ctx context.Context, id string) error {
	reminderID, err := parseTaskID(id)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM reminders WHERE id = $1`, reminderID)
	if err != nil {
		return fmt.Errorf("delete reminder: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RunDueReminders delivers every pending reminder whose send_at is at or
// before now. Each reminder is claimed with SKIP LOCKED and the notification is
// sent before its status commits, so replicas never double-send; a crash
// between the two retries it on the next tick. Failures are retried on later
// ticks until MaxReminderAttempts, then the reminder is marked failed.
func (s *Store) RunDueReminders(ctx context.Context, now time.Time, notify schedules.Notifier) (schedules.ReminderRunResult, error) {
	var result schedules.ReminderRunResult
	for range maxRemindersPerTick {
		processed, err := s.runOneDueReminder(ctx, now, notify, &result)
		if err != nil || !processed {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) runOneDueReminder(ctx context.Context, now time.Time, notify schedules.Notifier, result *schedules.ReminderRunResult) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin reminder run: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		id, attempts          int
		title, message, sound string
		priority              int
	)
	// updated_at < now keeps a reminder that just failed from being retried in the same tick.
	err = tx.QueryRow(ctx, `
		SELECT id, title, message, priority, sound, attempts
		FROM reminders
		WHERE status = 'pending' AND send_at <= $1 AND (attempts = 0 OR updated_at < $1)
		ORDER BY send_at, id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, now,
	).Scan(&id, &title, &message, &priority, &sound, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim reminder: %w", err)
	}

	reminderID := strconv.Itoa(id)
	sendErr := notify(ctx, schedules.Notification{Title: title, Message: message, Priority: priority, Sound: sound})
	attempts++
	switch {
	case sendErr == nil:
		_, err = tx.Exec(ctx, `
			UPDATE reminders SET status = 'sent', attempts = $2, last_error = '', sent_at = $3, updated_at = $3
			WHERE id = $1`, id, attempts, now)
		result.Sent = append(result.Sent, reminderID)
	case attempts >= schedules.MaxReminderAttempts:
		_, err = tx.Exec(ctx, `
			UPDATE reminders SET status = 'failed', attempts = $2, last_error = $3, updated_at = $4
			WHERE id = $1`, id, attempts, sendErr.Error(), now)
		result.Failed = append(result.Failed, reminderID)
	default:
		_, err = tx.Exec(ctx, `
			UPDATE reminders SET attempts = $2, last_error = $3, updated_at = $4
			WHERE id = $1`, id, attempts, sendErr.Error(), now)
		result.Retrying = append(result.Retrying, reminderID)
	}
	if err != nil {
		return false, fmt.Errorf("record reminder delivery: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit reminder run: %w", err)
	}
	return true, nil
}

type reminderTemplate struct {
	Title    string
	Message  string
	Priority int
	Sound    string
	SendAt   time.Time
}

// validateReminderTemplate trims and validates fields in place, mirroring the
// Pushover limits. A zero now skips the send_at-in-the-past check.
func validateReminderTemplate(template *reminderTemplate, now time.Time) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %w: %s", ErrInvalidArgument, schedules.ErrInvalidNotification, reason)
	}
	template.Title = strings.TrimSpace(template.Title)
	if utf8.RuneCountInString(template.Title) > 250 {
		return invalid("title must be at most 250 characters")
	}
	template.Message = strings.TrimSpace(template.Message)
	if template.Message == "" || utf8.RuneCountInString(template.Message) > 1024 {
		return invalid("message is required and must be at most 1024 characters")
	}
	template.Sound = strings.TrimSpace(template.Sound)
	if err := validateNotification(template.Priority, template.Sound); err != nil {
		return err
	}
	if template.SendAt.IsZero() {
		return invalid("send_at is required")
	}
	if !now.IsZero() && template.SendAt.Before(now.Add(-reminderPastTolerance)) {
		return invalid("send_at is in the past")
	}
	template.SendAt = template.SendAt.UTC()
	return nil
}

func scanReminder(row taskScanner) (schedules.Reminder, error) {
	var reminder schedules.Reminder
	if err := row.Scan(
		&reminder.ID, &reminder.Title, &reminder.Message, &reminder.Priority, &reminder.Sound, &reminder.SendAt,
		&reminder.Status, &reminder.Attempts, &reminder.LastError, &reminder.SentAt, &reminder.CreatedAt, &reminder.UpdatedAt,
	); err != nil {
		return schedules.Reminder{}, err
	}
	return reminder, nil
}
