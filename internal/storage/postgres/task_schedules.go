package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/pagination"
	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/leofelipet/contexta/internal/tasks"
)

// maxSchedulesPerTick bounds one tick so a large backlog cannot hold the worker forever;
// the remainder is picked up on the next tick.
const maxSchedulesPerTick = 500

const maxDueInMinutes = 366 * 24 * 60

const scheduleSelectCols = `
	id::text, cron, timezone, enabled, skip_if_open, title, description, company, due_in_minutes,
	COALESCE(conversation_id::text, ''), COALESCE(contact_id::text, ''),
	next_run_at, last_run_at, COALESCE(last_task_id::text, ''), run_count, created_at, updated_at`

func (s *Store) ListTaskSchedules(ctx context.Context, params schedules.ListParams) (schedules.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "task_schedules")
	if err != nil {
		return schedules.Page{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isTaskID(cursor.ID)) {
		return schedules.Page{}, pagination.ErrInvalidCursor
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+scheduleSelectCols+`
		FROM task_schedules
		WHERE ($1::timestamptz IS NULL OR (created_at, id) < ($1, $2::bigint))
		  AND ($3::boolean IS NULL OR enabled = $3)
		  AND ($4 = '' OR title ILIKE '%' || $4 || '%' OR description ILIKE '%' || $4 || '%' OR id::text = $4)
		ORDER BY created_at DESC, id DESC
		LIMIT $5`,
		nullableTime(cursor.Time), nullableTaskID(cursor.ID), params.Enabled,
		strings.TrimPrefix(strings.TrimSpace(params.Query), "#"), limit+1,
	)
	if err != nil {
		return schedules.Page{}, fmt.Errorf("list task schedules: %w", err)
	}
	defer rows.Close()

	items := make([]schedules.Schedule, 0, limit+1)
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return schedules.Page{}, err
		}
		items = append(items, schedule)
	}
	if err := rows.Err(); err != nil {
		return schedules.Page{}, fmt.Errorf("iterate task schedules: %w", err)
	}

	page := schedules.Page{Schedules: items[:min(limit, len(items))]}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "task_schedules", Time: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetTaskSchedule(ctx context.Context, id string) (schedules.Schedule, error) {
	scheduleID, err := parseTaskID(id)
	if err != nil {
		return schedules.Schedule{}, err
	}
	row := s.pool.QueryRow(ctx, `SELECT `+scheduleSelectCols+` FROM task_schedules WHERE id = $1`, scheduleID)
	schedule, err := scanSchedule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return schedules.Schedule{}, ErrNotFound
	}
	if err != nil {
		return schedules.Schedule{}, fmt.Errorf("get task schedule: %w", err)
	}
	return schedule, nil
}

func (s *Store) CreateTaskSchedule(ctx context.Context, params schedules.CreateParams) (schedules.Schedule, error) {
	timezone := strings.TrimSpace(params.Timezone)
	if timezone == "" {
		timezone = schedules.DefaultTimezone
	}
	enabled := params.Enabled == nil || *params.Enabled
	template := scheduleTemplate{
		Cron: strings.TrimSpace(params.Cron), Timezone: timezone,
		Title: params.Title, Description: params.Description, Company: params.Company,
		DueInMinutes: params.DueInMinutes, ConversationID: params.ConversationID, ContactID: params.ContactID,
	}
	if err := s.validateScheduleTemplate(ctx, &template); err != nil {
		return schedules.Schedule{}, err
	}
	nextRunAt, err := scheduleNextRun(template.Cron, template.Timezone, enabled)
	if err != nil {
		return schedules.Schedule{}, err
	}

	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO task_schedules (
			cron, timezone, enabled, skip_if_open, title, description, company,
			due_in_minutes, conversation_id, contact_id, next_run_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::uuid, $10::uuid, $11)
		RETURNING id::text`,
		template.Cron, template.Timezone, enabled, params.SkipIfOpen,
		template.Title, template.Description, template.Company, template.DueInMinutes,
		nullableUUID(template.ConversationID), nullableUUID(template.ContactID), nextRunAt,
	).Scan(&id)
	if err != nil {
		return schedules.Schedule{}, fmt.Errorf("create task schedule: %w", err)
	}
	return s.GetTaskSchedule(ctx, id)
}

func (s *Store) UpdateTaskSchedule(ctx context.Context, id string, params schedules.UpdateParams) (schedules.Schedule, error) {
	scheduleID, err := parseTaskID(id)
	if err != nil {
		return schedules.Schedule{}, err
	}
	current, err := s.GetTaskSchedule(ctx, id)
	if err != nil {
		return schedules.Schedule{}, err
	}

	template := scheduleTemplate{
		Cron: current.Cron, Timezone: current.Timezone,
		Title: current.Title, Description: current.Description, Company: current.Company,
		DueInMinutes: current.DueInMinutes, ConversationID: current.ConversationID, ContactID: current.ContactID,
	}
	if params.Cron != nil {
		template.Cron = strings.TrimSpace(*params.Cron)
	}
	if params.Timezone != nil {
		template.Timezone = strings.TrimSpace(*params.Timezone)
		if template.Timezone == "" {
			template.Timezone = schedules.DefaultTimezone
		}
	}
	if params.Title != nil {
		template.Title = *params.Title
	}
	if params.Description != nil {
		template.Description = *params.Description
	}
	if params.Company != nil {
		template.Company = *params.Company
	}
	if params.DueInMinutes != nil {
		template.DueInMinutes = params.DueInMinutes
		if *params.DueInMinutes == 0 {
			template.DueInMinutes = nil
		}
	}
	if params.ConversationID != nil {
		template.ConversationID = *params.ConversationID
	}
	if params.ContactID != nil {
		template.ContactID = *params.ContactID
	}
	if err := s.validateScheduleTemplate(ctx, &template); err != nil {
		return schedules.Schedule{}, err
	}

	enabled := current.Enabled
	if params.Enabled != nil {
		enabled = *params.Enabled
	}
	skipIfOpen := current.SkipIfOpen
	if params.SkipIfOpen != nil {
		skipIfOpen = *params.SkipIfOpen
	}

	// Only recompute when timing changes: recomputing on a plain title edit would
	// drop a run that is due but not yet picked up by the next tick.
	nextRunAt := current.NextRunAt
	timingChanged := template.Cron != current.Cron || template.Timezone != current.Timezone
	if !enabled {
		nextRunAt = nil
	} else if timingChanged || !current.Enabled || nextRunAt == nil {
		nextRunAt, err = scheduleNextRun(template.Cron, template.Timezone, true)
		if err != nil {
			return schedules.Schedule{}, err
		}
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE task_schedules SET
			cron = $2, timezone = $3, enabled = $4, skip_if_open = $5,
			title = $6, description = $7, company = $8, due_in_minutes = $9,
			conversation_id = $10::uuid, contact_id = $11::uuid, next_run_at = $12,
			updated_at = now()
		WHERE id = $1`,
		scheduleID, template.Cron, template.Timezone, enabled, skipIfOpen,
		template.Title, template.Description, template.Company, template.DueInMinutes,
		nullableUUID(template.ConversationID), nullableUUID(template.ContactID), nextRunAt,
	)
	if err != nil {
		return schedules.Schedule{}, fmt.Errorf("update task schedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return schedules.Schedule{}, ErrNotFound
	}
	return s.GetTaskSchedule(ctx, id)
}

// DeleteTaskSchedule removes the schedule; tasks it already created are kept
// and lose their schedule_id.
func (s *Store) DeleteTaskSchedule(ctx context.Context, id string) error {
	scheduleID, err := parseTaskID(id)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM task_schedules WHERE id = $1`, scheduleID)
	if err != nil {
		return fmt.Errorf("delete task schedule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RunDueTaskSchedules creates one task for every enabled schedule whose
// next_run_at is at or before now. Runs missed while the server was down are
// collapsed into a single task. Each schedule is claimed with SKIP LOCKED in
// its own transaction, so concurrent replicas never double-fire.
func (s *Store) RunDueTaskSchedules(ctx context.Context, now time.Time) (schedules.RunResult, error) {
	var result schedules.RunResult
	for range maxSchedulesPerTick {
		processed, err := s.runOneDueTaskSchedule(ctx, now, &result)
		if err != nil || !processed {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) runOneDueTaskSchedule(ctx context.Context, now time.Time, result *schedules.RunResult) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin task schedule run: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		id, taskID                                      int64
		cronExpr, timezone, title, description, company string
		conversationID, contactID, lastTaskStatus       string
		skipIfOpen                                      bool
		dueInMinutes                                    *int
	)
	err = tx.QueryRow(ctx, `
		SELECT s.id, s.cron, s.timezone, s.skip_if_open, s.title, s.description, s.company,
		       s.due_in_minutes, COALESCE(s.conversation_id::text, ''), COALESCE(s.contact_id::text, ''),
		       COALESCE((SELECT t.status FROM tasks t WHERE t.id = s.last_task_id), '')
		FROM task_schedules s
		WHERE s.enabled AND s.next_run_at <= $1
		ORDER BY s.next_run_at, s.id
		LIMIT 1
		FOR UPDATE OF s SKIP LOCKED`, now,
	).Scan(&id, &cronExpr, &timezone, &skipIfOpen, &title, &description, &company,
		&dueInMinutes, &conversationID, &contactID, &lastTaskStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim task schedule: %w", err)
	}

	nextRunAt, err := schedules.Next(cronExpr, timezone, now)
	if err != nil {
		// Should not happen since input is validated, but a tzdata change could
		// invalidate a stored timezone; disable instead of retrying every tick.
		if _, err := tx.Exec(ctx, `UPDATE task_schedules SET enabled = false, next_run_at = NULL, updated_at = now() WHERE id = $1`, id); err != nil {
			return false, fmt.Errorf("disable task schedule: %w", err)
		}
		result.Failed = append(result.Failed, strconv.FormatInt(id, 10))
		return true, tx.Commit(ctx)
	}

	if skipIfOpen && lastTaskStatus != "" && !tasks.IsClosed(lastTaskStatus) {
		if _, err := tx.Exec(ctx, `UPDATE task_schedules SET next_run_at = $2 WHERE id = $1`, id, nextRunAt); err != nil {
			return false, fmt.Errorf("advance task schedule: %w", err)
		}
		result.Skipped = append(result.Skipped, strconv.FormatInt(id, 10))
		return true, tx.Commit(ctx)
	}

	var dueAt *time.Time
	if dueInMinutes != nil {
		value := now.Add(time.Duration(*dueInMinutes) * time.Minute)
		dueAt = &value
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO tasks (title, description, company, status, due_at, conversation_id, contact_id, schedule_id)
		VALUES ($1, $2, $3, $4, $5, $6::uuid, $7::uuid, $8)
		RETURNING id`,
		title, description, company, tasks.StatusPending, dueAt,
		nullableUUID(conversationID), nullableUUID(contactID), id,
	).Scan(&taskID)
	if err != nil {
		return false, fmt.Errorf("create scheduled task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_schedules SET
			next_run_at = $2, last_run_at = $3, last_task_id = $4, run_count = run_count + 1
		WHERE id = $1`, id, nextRunAt, now, taskID); err != nil {
		return false, fmt.Errorf("advance task schedule: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit task schedule run: %w", err)
	}
	result.Created = append(result.Created, strconv.FormatInt(taskID, 10))
	return true, nil
}

type scheduleTemplate struct {
	Cron           string
	Timezone       string
	Title          string
	Description    string
	Company        string
	DueInMinutes   *int
	ConversationID string
	ContactID      string
}

// validateScheduleTemplate trims and validates fields in place, mirroring the task limits.
func (s *Store) validateScheduleTemplate(ctx context.Context, template *scheduleTemplate) error {
	if err := schedules.Validate(template.Cron, template.Timezone); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	template.Title = strings.TrimSpace(template.Title)
	if template.Title == "" || len(template.Title) > 500 {
		return ErrInvalidArgument
	}
	template.Description = strings.TrimSpace(template.Description)
	if len(template.Description) > 10000 {
		return ErrInvalidArgument
	}
	template.Company = strings.TrimSpace(template.Company)
	if len(template.Company) > 200 {
		return ErrInvalidArgument
	}
	if template.DueInMinutes != nil && (*template.DueInMinutes < 1 || *template.DueInMinutes > maxDueInMinutes) {
		return ErrInvalidArgument
	}
	template.ConversationID = strings.TrimSpace(template.ConversationID)
	if template.ConversationID != "" {
		if err := s.requireEntity(ctx, "conversations", template.ConversationID); err != nil {
			return err
		}
	}
	template.ContactID = strings.TrimSpace(template.ContactID)
	if template.ContactID != "" {
		if err := s.requireEntity(ctx, "contacts", template.ContactID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) requireEntity(ctx context.Context, table, id string) error {
	if !isUUID(id) {
		return ErrInvalidArgument
	}
	exists, err := s.entityExists(ctx, table, id)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func scheduleNextRun(cronExpr, timezone string, enabled bool) (*time.Time, error) {
	if !enabled {
		return nil, nil
	}
	next, err := schedules.Next(cronExpr, timezone, time.Now())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	return &next, nil
}

func scanSchedule(row taskScanner) (schedules.Schedule, error) {
	var schedule schedules.Schedule
	if err := row.Scan(
		&schedule.ID, &schedule.Cron, &schedule.Timezone, &schedule.Enabled, &schedule.SkipIfOpen,
		&schedule.Title, &schedule.Description, &schedule.Company, &schedule.DueInMinutes,
		&schedule.ConversationID, &schedule.ContactID,
		&schedule.NextRunAt, &schedule.LastRunAt, &schedule.LastTaskID, &schedule.RunCount,
		&schedule.CreatedAt, &schedule.UpdatedAt,
	); err != nil {
		return schedules.Schedule{}, err
	}
	return schedule, nil
}
