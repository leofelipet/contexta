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
	"github.com/leofelipet/contexta/internal/tasks"
)

const taskSelectCols = `
	t.id::text, t.title, t.description, t.company, t.status, t.due_at,
	COALESCE(t.conversation_id::text, ''), COALESCE(t.contact_id::text, ''),
	COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''),
	         NULLIF(cc.phone, ''), NULLIF(c.provider_conversation_id, ''), ''),
	COALESCE(NULLIF(ct.name, ''), NULLIF(ct.push_name, ''), NULLIF(ct.phone, ''),
	         NULLIF(ct.provider_contact_id, ''), ''),
	t.created_at, t.updated_at`

const taskJoins = `
	FROM tasks t
	LEFT JOIN conversations c ON c.id = t.conversation_id
	LEFT JOIN contacts cc ON cc.id = c.contact_id
	LEFT JOIN contacts ct ON ct.id = t.contact_id`

func (s *Store) ListTasks(ctx context.Context, params tasks.ListParams) (tasks.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "tasks")
	if err != nil {
		return tasks.Page{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isTaskID(cursor.ID)) {
		return tasks.Page{}, pagination.ErrInvalidCursor
	}
	if params.Status != "" && !tasks.ValidStatus(params.Status) {
		return tasks.Page{}, ErrInvalidArgument
	}
	if params.ContactID != "" && !isUUID(params.ContactID) {
		return tasks.Page{}, ErrInvalidArgument
	}
	if params.ConversationID != "" && !isUUID(params.ConversationID) {
		return tasks.Page{}, ErrInvalidArgument
	}

	query := strings.TrimSpace(params.Query)
	company := strings.TrimSpace(params.Company)

	var queryID any
	textQuery := query
	openOnly := params.OpenOnly
	if strings.HasPrefix(query, "#") {
		idPart := strings.TrimSpace(strings.TrimPrefix(query, "#"))
		if isTaskID(idPart) {
			parsed, err := parseTaskID(idPart)
			if err != nil {
				return tasks.Page{}, ErrInvalidArgument
			}
			queryID = parsed
			textQuery = ""
			openOnly = false // exact ID lookup should find closed tasks too
		}
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+taskSelectCols+taskJoins+`
		WHERE ($1::timestamptz IS NULL OR (t.created_at, t.id) < ($1, $2::bigint))
		  AND ($3 = '' OR t.status = $3)
		  AND ($4 = '' OR t.company ILIKE '%' || $4 || '%')
		  AND ($5::uuid IS NULL OR t.contact_id = $5::uuid)
		  AND ($6::uuid IS NULL OR t.conversation_id = $6::uuid)
		  AND ($7::bigint IS NULL OR t.id = $7::bigint)
		  AND ($8 = '' OR t.title ILIKE '%' || $8 || '%' OR t.description ILIKE '%' || $8 || '%' OR t.id::text = $8)
		  AND (
		    NOT $9 OR (
		      t.due_at IS NOT NULL
		      AND t.due_at < now()
		      AND t.status IN ('pending', 'in_progress', 'blocked')
		    )
		  )
		  AND (
		    NOT $10 OR $3 <> '' OR t.status NOT IN ('done', 'cancelled')
		  )
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT $11`,
		nullableTime(cursor.Time), nullableTaskID(cursor.ID),
		params.Status, company,
		nullableUUID(params.ContactID), nullableUUID(params.ConversationID),
		queryID, textQuery, params.Overdue, openOnly, limit+1,
	)
	if err != nil {
		return tasks.Page{}, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	items := make([]tasks.Task, 0, limit+1)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return tasks.Page{}, err
		}
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		return tasks.Page{}, fmt.Errorf("iterate tasks: %w", err)
	}

	page := tasks.Page{Tasks: items[:min(limit, len(items))]}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "tasks", Time: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetTask(ctx context.Context, id string) (tasks.Task, error) {
	taskID, err := parseTaskID(id)
	if err != nil {
		return tasks.Task{}, err
	}
	row := s.pool.QueryRow(ctx, `SELECT `+taskSelectCols+taskJoins+` WHERE t.id = $1`, taskID)
	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return tasks.Task{}, ErrNotFound
	}
	if err != nil {
		return tasks.Task{}, fmt.Errorf("get task: %w", err)
	}
	memories, err := s.ListTaskMemories(ctx, id)
	if err != nil {
		return tasks.Task{}, err
	}
	task.Memories = memories
	return task, nil
}

func (s *Store) CreateTask(ctx context.Context, params tasks.CreateParams) (tasks.Task, error) {
	title := strings.TrimSpace(params.Title)
	if title == "" || len(title) > 500 {
		return tasks.Task{}, ErrInvalidArgument
	}
	description := strings.TrimSpace(params.Description)
	if len(description) > 10000 {
		return tasks.Task{}, ErrInvalidArgument
	}
	company := strings.TrimSpace(params.Company)
	if len(company) > 200 {
		return tasks.Task{}, ErrInvalidArgument
	}
	status := strings.TrimSpace(params.Status)
	if status == "" {
		status = tasks.StatusPending
	}
	if !tasks.ValidStatus(status) {
		return tasks.Task{}, ErrInvalidArgument
	}
	conversationID := strings.TrimSpace(params.ConversationID)
	contactID := strings.TrimSpace(params.ContactID)
	if conversationID != "" {
		if !isUUID(conversationID) {
			return tasks.Task{}, ErrInvalidArgument
		}
		exists, err := s.entityExists(ctx, "conversations", conversationID)
		if err != nil {
			return tasks.Task{}, err
		}
		if !exists {
			return tasks.Task{}, ErrNotFound
		}
	}
	if contactID != "" {
		if !isUUID(contactID) {
			return tasks.Task{}, ErrInvalidArgument
		}
		exists, err := s.entityExists(ctx, "contacts", contactID)
		if err != nil {
			return tasks.Task{}, err
		}
		if !exists {
			return tasks.Task{}, ErrNotFound
		}
	}

	dueAt := params.DueAt
	if status == tasks.StatusDone {
		now := time.Now().UTC()
		dueAt = &now
	}

	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tasks (title, description, company, status, due_at, conversation_id, contact_id)
		VALUES ($1, $2, $3, $4, $5, $6::uuid, $7::uuid)
		RETURNING id::text`,
		title, description, company, status, dueAt,
		nullableUUID(conversationID), nullableUUID(contactID),
	).Scan(&id)
	if err != nil {
		return tasks.Task{}, fmt.Errorf("create task: %w", err)
	}
	return s.GetTask(ctx, id)
}

func (s *Store) UpdateTask(ctx context.Context, id string, params tasks.UpdateParams) (tasks.Task, tasks.MemoryCleanup, error) {
	var cleanup tasks.MemoryCleanup
	taskID, err := parseTaskID(id)
	if err != nil {
		return tasks.Task{}, cleanup, err
	}

	current, err := s.GetTask(ctx, id)
	if err != nil {
		return tasks.Task{}, cleanup, err
	}

	title := current.Title
	if params.Title != nil {
		title = strings.TrimSpace(*params.Title)
		if title == "" || len(title) > 500 {
			return tasks.Task{}, cleanup, ErrInvalidArgument
		}
	}
	description := current.Description
	if params.Description != nil {
		description = strings.TrimSpace(*params.Description)
		if len(description) > 10000 {
			return tasks.Task{}, cleanup, ErrInvalidArgument
		}
	}
	company := current.Company
	if params.Company != nil {
		company = strings.TrimSpace(*params.Company)
		if len(company) > 200 {
			return tasks.Task{}, cleanup, ErrInvalidArgument
		}
	}
	status := current.Status
	if params.Status != nil {
		status = strings.TrimSpace(*params.Status)
		if !tasks.ValidStatus(status) {
			return tasks.Task{}, cleanup, ErrInvalidArgument
		}
	}

	dueAt := current.DueAt
	if params.DueAt != nil {
		value := strings.TrimSpace(*params.DueAt)
		if value == "" {
			dueAt = nil
		} else {
			parsed, err := parseTaskDueAt(value)
			if err != nil {
				return tasks.Task{}, cleanup, ErrInvalidArgument
			}
			dueAt = parsed
		}
	}

	conversationID := current.ConversationID
	if params.ConversationID != nil {
		conversationID = strings.TrimSpace(*params.ConversationID)
		if conversationID != "" {
			if !isUUID(conversationID) {
				return tasks.Task{}, cleanup, ErrInvalidArgument
			}
			exists, err := s.entityExists(ctx, "conversations", conversationID)
			if err != nil {
				return tasks.Task{}, cleanup, err
			}
			if !exists {
				return tasks.Task{}, cleanup, ErrNotFound
			}
		}
	}

	contactID := current.ContactID
	if params.ContactID != nil {
		contactID = strings.TrimSpace(*params.ContactID)
		if contactID != "" {
			if !isUUID(contactID) {
				return tasks.Task{}, cleanup, ErrInvalidArgument
			}
			exists, err := s.entityExists(ctx, "contacts", contactID)
			if err != nil {
				return tasks.Task{}, cleanup, err
			}
			if !exists {
				return tasks.Task{}, cleanup, ErrNotFound
			}
		}
	}

	if params.DeleteMemories && !tasks.IsClosed(status) {
		return tasks.Task{}, cleanup, ErrInvalidArgument
	}

	if status == tasks.StatusDone && current.Status != tasks.StatusDone {
		now := time.Now().UTC()
		dueAt = &now
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tasks.Task{}, cleanup, fmt.Errorf("begin update task: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		UPDATE tasks SET
			title = $2,
			description = $3,
			company = $4,
			status = $5,
			due_at = $6,
			conversation_id = $7::uuid,
			contact_id = $8::uuid,
			updated_at = now()
		WHERE id = $1`,
		taskID, title, description, company, status, dueAt,
		nullableUUID(conversationID), nullableUUID(contactID),
	)
	if err != nil {
		return tasks.Task{}, cleanup, fmt.Errorf("update task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tasks.Task{}, cleanup, ErrNotFound
	}
	if params.DeleteMemories {
		// Kept memories stay linked: the task still exists and they remain useful context.
		cleanup, err = deleteExclusiveTaskMemories(ctx, tx, taskID)
		if err != nil {
			return tasks.Task{}, tasks.MemoryCleanup{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return tasks.Task{}, tasks.MemoryCleanup{}, fmt.Errorf("commit update task: %w", err)
	}
	task, err := s.GetTask(ctx, id)
	return task, cleanup, err
}

func (s *Store) DeleteTask(ctx context.Context, id string, deleteMemories bool) (tasks.DeleteResult, error) {
	taskID, err := parseTaskID(id)
	if err != nil {
		return tasks.DeleteResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tasks.DeleteResult{}, fmt.Errorf("begin delete task: %w", err)
	}
	defer tx.Rollback(ctx)

	var cleanup tasks.MemoryCleanup
	if deleteMemories {
		// Runs before the task delete so the links are still there to inspect.
		cleanup, err = deleteExclusiveTaskMemories(ctx, tx, taskID)
		if err != nil {
			return tasks.DeleteResult{}, err
		}
	}

	tag, err := tx.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, taskID)
	if err != nil {
		return tasks.DeleteResult{}, fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tasks.DeleteResult{}, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return tasks.DeleteResult{}, fmt.Errorf("commit delete task: %w", err)
	}
	return tasks.DeleteResult{Deleted: true, MemoryCleanup: cleanup}, nil
}

// deleteExclusiveTaskMemories deletes memories linked only to taskID and
// reports the ones kept because another task also links them.
func deleteExclusiveTaskMemories(ctx context.Context, tx pgx.Tx, taskID int64) (tasks.MemoryCleanup, error) {
	// Lock the linked memories first so a concurrent attach to another task
	// (whose FK check needs a share lock on the memory row) waits for us.
	if _, err := tx.Exec(ctx, `
		SELECT m.id
		FROM memories m
		JOIN task_memories tm ON tm.memory_id = m.id
		WHERE tm.task_id = $1
		ORDER BY m.id
		FOR UPDATE OF m`, taskID); err != nil {
		return tasks.MemoryCleanup{}, fmt.Errorf("lock task memories: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT tm.memory_id::text,
		       EXISTS (
		           SELECT 1 FROM task_memories other
		           WHERE other.memory_id = tm.memory_id AND other.task_id <> tm.task_id
		       )
		FROM task_memories tm
		WHERE tm.task_id = $1
		ORDER BY tm.created_at DESC, tm.memory_id DESC`, taskID)
	if err != nil {
		return tasks.MemoryCleanup{}, fmt.Errorf("list task memory links: %w", err)
	}
	var cleanup tasks.MemoryCleanup
	for rows.Next() {
		var memoryID string
		var shared bool
		if err := rows.Scan(&memoryID, &shared); err != nil {
			rows.Close()
			return tasks.MemoryCleanup{}, err
		}
		if shared {
			cleanup.KeptMemoryIDs = append(cleanup.KeptMemoryIDs, memoryID)
		} else {
			cleanup.DeletedMemoryIDs = append(cleanup.DeletedMemoryIDs, memoryID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return tasks.MemoryCleanup{}, fmt.Errorf("iterate task memory links: %w", err)
	}

	if len(cleanup.DeletedMemoryIDs) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM memories WHERE id = ANY($1::uuid[])`, cleanup.DeletedMemoryIDs); err != nil {
			return tasks.MemoryCleanup{}, fmt.Errorf("delete task memories: %w", err)
		}
	}
	return cleanup, nil
}

func (s *Store) ListTaskMemories(ctx context.Context, taskID string) ([]tasks.MemoryRef, error) {
	id, err := parseTaskID(taskID)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.id::text, m.title, m.source
		FROM task_memories tm
		JOIN memories m ON m.id = tm.memory_id
		WHERE tm.task_id = $1
		ORDER BY tm.created_at DESC, m.id DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("list task memories: %w", err)
	}
	defer rows.Close()

	items := make([]tasks.MemoryRef, 0)
	for rows.Next() {
		var item tasks.MemoryRef
		if err := rows.Scan(&item.ID, &item.Title, &item.Source); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task memories: %w", err)
	}
	return items, nil
}

func (s *Store) AttachTaskMemory(ctx context.Context, taskID, memoryID string) (tasks.Task, error) {
	id, err := parseTaskID(taskID)
	if err != nil {
		return tasks.Task{}, err
	}
	if !isUUID(memoryID) {
		return tasks.Task{}, ErrInvalidArgument
	}
	exists, err := s.taskExists(ctx, id)
	if err != nil {
		return tasks.Task{}, err
	}
	if !exists {
		return tasks.Task{}, ErrNotFound
	}
	exists, err = s.entityExists(ctx, "memories", memoryID)
	if err != nil {
		return tasks.Task{}, err
	}
	if !exists {
		return tasks.Task{}, ErrNotFound
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO task_memories (task_id, memory_id)
		VALUES ($1, $2::uuid)
		ON CONFLICT DO NOTHING`, id, memoryID)
	if err != nil {
		return tasks.Task{}, fmt.Errorf("attach task memory: %w", err)
	}
	return s.GetTask(ctx, taskID)
}

func (s *Store) DetachTaskMemory(ctx context.Context, taskID, memoryID string) (tasks.Task, error) {
	id, err := parseTaskID(taskID)
	if err != nil {
		return tasks.Task{}, err
	}
	if !isUUID(memoryID) {
		return tasks.Task{}, ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM task_memories
		WHERE task_id = $1 AND memory_id = $2::uuid`, id, memoryID)
	if err != nil {
		return tasks.Task{}, fmt.Errorf("detach task memory: %w", err)
	}
	if tag.RowsAffected() == 0 {
		exists, err := s.taskExists(ctx, id)
		if err != nil {
			return tasks.Task{}, err
		}
		if !exists {
			return tasks.Task{}, ErrNotFound
		}
		return tasks.Task{}, ErrNotFound
	}
	return s.GetTask(ctx, taskID)
}

func (s *Store) taskExists(ctx context.Context, id int64) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check tasks exists: %w", err)
	}
	return exists, nil
}

func (s *Store) entityExists(ctx context.Context, table, id string) (bool, error) {
	var exists bool
	query := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1)`, table)
	if err := s.pool.QueryRow(ctx, query, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check %s exists: %w", table, err)
	}
	return exists, nil
}

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(row taskScanner) (tasks.Task, error) {
	var task tasks.Task
	var dueAt *time.Time
	if err := row.Scan(
		&task.ID, &task.Title, &task.Description, &task.Company, &task.Status, &dueAt,
		&task.ConversationID, &task.ContactID, &task.ConversationTitle, &task.ContactName,
		&task.CreatedAt, &task.UpdatedAt,
	); err != nil {
		return tasks.Task{}, err
	}
	task.DueAt = dueAt
	return task, nil
}

func parseTaskDueAt(value string) (*time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		utc := parsed.UTC()
		return &utc, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	utc := parsed.UTC()
	return &utc, nil
}

func parseTaskID(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, ErrInvalidArgument
	}
	id, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || id < 1 {
		return 0, ErrInvalidArgument
	}
	return id, nil
}

func isTaskID(value string) bool {
	_, err := parseTaskID(value)
	return err == nil
}

func nullableTaskID(value string) any {
	id, err := parseTaskID(value)
	if err != nil {
		return nil
	}
	return id
}
