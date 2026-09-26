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

func (s *Store) UpdateTask(ctx context.Context, id string, params tasks.UpdateParams) (tasks.Task, error) {
	taskID, err := parseTaskID(id)
	if err != nil {
		return tasks.Task{}, err
	}

	current, err := s.GetTask(ctx, id)
	if err != nil {
		return tasks.Task{}, err
	}

	title := current.Title
	if params.Title != nil {
		title = strings.TrimSpace(*params.Title)
		if title == "" || len(title) > 500 {
			return tasks.Task{}, ErrInvalidArgument
		}
	}
	description := current.Description
	if params.Description != nil {
		description = strings.TrimSpace(*params.Description)
		if len(description) > 10000 {
			return tasks.Task{}, ErrInvalidArgument
		}
	}
	company := current.Company
	if params.Company != nil {
		company = strings.TrimSpace(*params.Company)
		if len(company) > 200 {
			return tasks.Task{}, ErrInvalidArgument
		}
	}
	status := current.Status
	if params.Status != nil {
		status = strings.TrimSpace(*params.Status)
		if !tasks.ValidStatus(status) {
			return tasks.Task{}, ErrInvalidArgument
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
				return tasks.Task{}, ErrInvalidArgument
			}
			dueAt = parsed
		}
	}

	conversationID := current.ConversationID
	if params.ConversationID != nil {
		conversationID = strings.TrimSpace(*params.ConversationID)
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
	}

	contactID := current.ContactID
	if params.ContactID != nil {
		contactID = strings.TrimSpace(*params.ContactID)
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
	}

	if status == tasks.StatusDone && current.Status != tasks.StatusDone {
		now := time.Now().UTC()
		dueAt = &now
	}

	tag, err := s.pool.Exec(ctx, `
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
		return tasks.Task{}, fmt.Errorf("update task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tasks.Task{}, ErrNotFound
	}
	return s.GetTask(ctx, id)
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	taskID, err := parseTaskID(id)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, taskID)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
