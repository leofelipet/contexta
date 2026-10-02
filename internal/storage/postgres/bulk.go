package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leofelipet/contexta/internal/tasks"
)

// maxBulkIDs caps how many rows one bulk operation may touch.
const maxBulkIDs = 100

// cleanBulkUUIDs trims and dedupes ids, requiring one to maxBulkIDs valid UUIDs.
func cleanBulkUUIDs(ids []string) ([]string, error) {
	cleaned := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !isUUID(id) {
			return nil, ErrInvalidArgument
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	if len(cleaned) == 0 || len(cleaned) > maxBulkIDs {
		return nil, ErrInvalidArgument
	}
	return cleaned, nil
}

// cleanBulkNumericIDs does the same for the sequential IDs of tasks, schedules,
// and companies.
func cleanBulkNumericIDs(ids []string) ([]int64, error) {
	cleaned := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, value := range ids {
		if strings.TrimSpace(value) == "" {
			continue
		}
		id, err := parseTaskID(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	if len(cleaned) == 0 || len(cleaned) > maxBulkIDs {
		return nil, ErrInvalidArgument
	}
	return cleaned, nil
}

// DeleteTasks deletes the given tasks; IDs that no longer exist are ignored.
// With deleteMemories, memories linked only to tasks in this set are deleted too.
func (s *Store) DeleteTasks(ctx context.Context, ids []string, deleteMemories bool) (tasks.BulkResult, error) {
	taskIDs, err := cleanBulkNumericIDs(ids)
	if err != nil {
		return tasks.BulkResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tasks.BulkResult{}, fmt.Errorf("begin bulk delete tasks: %w", err)
	}
	defer tx.Rollback(ctx)

	var cleanup tasks.MemoryCleanup
	if deleteMemories {
		// Runs before the task delete so the links are still there to inspect.
		cleanup, err = deleteExclusiveTaskMemories(ctx, tx, taskIDs)
		if err != nil {
			return tasks.BulkResult{}, err
		}
	}

	tag, err := tx.Exec(ctx, `DELETE FROM tasks WHERE id = ANY($1::bigint[])`, taskIDs)
	if err != nil {
		return tasks.BulkResult{}, fmt.Errorf("bulk delete tasks: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return tasks.BulkResult{}, fmt.Errorf("commit bulk delete tasks: %w", err)
	}
	return tasks.BulkResult{Count: int(tag.RowsAffected()), MemoryCleanup: cleanup}, nil
}

// UpdateTasksStatus sets the status of the given tasks, following the same
// rules as UpdateTask: moving a task to done sets its due_at to now, and
// deleteMemories requires a closing status.
func (s *Store) UpdateTasksStatus(ctx context.Context, ids []string, status string, deleteMemories bool) (tasks.BulkResult, error) {
	taskIDs, err := cleanBulkNumericIDs(ids)
	if err != nil {
		return tasks.BulkResult{}, err
	}
	status = strings.TrimSpace(status)
	if !tasks.ValidStatus(status) {
		return tasks.BulkResult{}, ErrInvalidArgument
	}
	if deleteMemories && !tasks.IsClosed(status) {
		return tasks.BulkResult{}, ErrInvalidArgument
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tasks.BulkResult{}, fmt.Errorf("begin bulk update tasks: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE tasks SET
			due_at = CASE WHEN $2 = 'done' AND status <> 'done' THEN now() ELSE due_at END,
			status = $2,
			updated_at = now()
		WHERE id = ANY($1::bigint[])`, taskIDs, status)
	if err != nil {
		return tasks.BulkResult{}, fmt.Errorf("bulk update tasks: %w", err)
	}
	var cleanup tasks.MemoryCleanup
	if deleteMemories {
		cleanup, err = deleteExclusiveTaskMemories(ctx, tx, taskIDs)
		if err != nil {
			return tasks.BulkResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return tasks.BulkResult{}, fmt.Errorf("commit bulk update tasks: %w", err)
	}
	return tasks.BulkResult{Count: int(tag.RowsAffected()), MemoryCleanup: cleanup}, nil
}

// DeleteMemories deletes the given memories and returns how many existed.
func (s *Store) DeleteMemories(ctx context.Context, ids []string) (int, error) {
	memoryIDs, err := cleanBulkUUIDs(ids)
	if err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM memories WHERE id = ANY($1::uuid[])`, memoryIDs)
	if err != nil {
		return 0, fmt.Errorf("bulk delete memories: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DeleteTaskSchedules deletes the given schedules; tasks they created are kept.
func (s *Store) DeleteTaskSchedules(ctx context.Context, ids []string) (int, error) {
	scheduleIDs, err := cleanBulkNumericIDs(ids)
	if err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM task_schedules WHERE id = ANY($1::bigint[])`, scheduleIDs)
	if err != nil {
		return 0, fmt.Errorf("bulk delete task schedules: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SetTaskSchedulesEnabled pauses or resumes the given schedules. Like
// UpdateTaskSchedule, a resumed schedule gets a fresh next run and a schedule
// that was already enabled keeps its pending one.
func (s *Store) SetTaskSchedulesEnabled(ctx context.Context, ids []string, enabled bool) (int, error) {
	scheduleIDs, err := cleanBulkNumericIDs(ids)
	if err != nil {
		return 0, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin bulk update task schedules: %w", err)
	}
	defer tx.Rollback(ctx)

	type scheduleTiming struct {
		id        int64
		cron      string
		timezone  string
		enabled   bool
		nextRunAt *time.Time
	}
	rows, err := tx.Query(ctx, `
		SELECT id, cron, timezone, enabled, next_run_at
		FROM task_schedules
		WHERE id = ANY($1::bigint[])
		ORDER BY id
		FOR UPDATE`, scheduleIDs)
	if err != nil {
		return 0, fmt.Errorf("lock task schedules: %w", err)
	}
	var timings []scheduleTiming
	for rows.Next() {
		var timing scheduleTiming
		if err := rows.Scan(&timing.id, &timing.cron, &timing.timezone, &timing.enabled, &timing.nextRunAt); err != nil {
			rows.Close()
			return 0, err
		}
		timings = append(timings, timing)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate task schedules: %w", err)
	}

	for _, timing := range timings {
		nextRunAt := timing.nextRunAt
		if !enabled {
			nextRunAt = nil
		} else if !timing.enabled || nextRunAt == nil {
			nextRunAt, err = scheduleNextRun(timing.cron, timing.timezone, true)
			if err != nil {
				return 0, err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE task_schedules SET enabled = $2, next_run_at = $3, updated_at = now()
			WHERE id = $1`, timing.id, enabled, nextRunAt); err != nil {
			return 0, fmt.Errorf("bulk update task schedule: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit bulk update task schedules: %w", err)
	}
	return len(timings), nil
}

// DeleteCompanies deletes the given companies; linked tasks, schedules, and
// contacts are kept and lose their company_id.
func (s *Store) DeleteCompanies(ctx context.Context, ids []string) (int, error) {
	companyIDs, err := cleanBulkNumericIDs(ids)
	if err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM companies WHERE id = ANY($1::bigint[])`, companyIDs)
	if err != nil {
		return 0, fmt.Errorf("bulk delete companies: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RemoveDenylistEntries removes the given entries so their targets are
// ingested again.
func (s *Store) RemoveDenylistEntries(ctx context.Context, ids []string) (int, error) {
	entryIDs, err := cleanBulkUUIDs(ids)
	if err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM denylist_entries WHERE id = ANY($1::uuid[])`, entryIDs)
	if err != nil {
		return 0, fmt.Errorf("bulk remove denylist entries: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
