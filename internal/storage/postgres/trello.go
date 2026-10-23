package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/tasks"
	"github.com/leofelipet/contexta/internal/trello"
)

func (s *Store) GetTrelloSettings(ctx context.Context) (trello.Settings, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT board_id, list_statuses, webhook_id, enabled, created_at, updated_at
		FROM trello_settings WHERE singleton = TRUE`)
	return scanTrelloSettings(row)
}

func (s *Store) GetTrelloCredentials(ctx context.Context) (trello.Settings, []byte, []byte, []byte, error) {
	var settings trello.Settings
	var listStatuses []byte
	var apiKey, applicationSecret, token []byte
	err := s.pool.QueryRow(ctx, `
		SELECT board_id, list_statuses, webhook_id, enabled, created_at, updated_at,
		       api_key_ciphertext, application_secret_ciphertext, token_ciphertext
		FROM trello_settings WHERE singleton = TRUE`,
	).Scan(&settings.BoardID, &listStatuses, &settings.WebhookID, &settings.Enabled, &settings.CreatedAt, &settings.UpdatedAt,
		&apiKey, &applicationSecret, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return trello.Settings{}, nil, nil, nil, trello.ErrNotConfigured
	}
	if err != nil {
		return trello.Settings{}, nil, nil, nil, fmt.Errorf("get Trello credentials: %w", err)
	}
	if err := json.Unmarshal(listStatuses, &settings.ListStatuses); err != nil {
		return trello.Settings{}, nil, nil, nil, fmt.Errorf("decode Trello list statuses: %w", err)
	}
	settings.Configured = true
	return settings, apiKey, applicationSecret, token, nil
}

func (s *Store) UpsertTrelloSettings(ctx context.Context, settings trello.Settings, apiKey, applicationSecret, token []byte) (trello.Settings, error) {
	listStatuses, err := json.Marshal(settings.ListStatuses)
	if err != nil {
		return trello.Settings{}, fmt.Errorf("encode Trello list statuses: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO trello_settings (
			singleton, api_key_ciphertext, application_secret_ciphertext, token_ciphertext,
			board_id, list_statuses, webhook_id, enabled
		)
		VALUES (TRUE, $1, $2, $3, $4, $5::jsonb, $6, $7)
		ON CONFLICT (singleton) DO UPDATE SET
			api_key_ciphertext = EXCLUDED.api_key_ciphertext,
			application_secret_ciphertext = EXCLUDED.application_secret_ciphertext,
			token_ciphertext = EXCLUDED.token_ciphertext,
			board_id = EXCLUDED.board_id,
			list_statuses = EXCLUDED.list_statuses,
			enabled = EXCLUDED.enabled,
			updated_at = now()
		RETURNING board_id, list_statuses, webhook_id, enabled, created_at, updated_at`,
		apiKey, applicationSecret, token, settings.BoardID, listStatuses, settings.WebhookID, settings.Enabled)
	result, err := scanTrelloSettings(row)
	if err != nil {
		return trello.Settings{}, fmt.Errorf("upsert Trello settings: %w", err)
	}
	return result, nil
}

func (s *Store) SetTrelloWebhookID(ctx context.Context, webhookID string) (trello.Settings, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE trello_settings SET webhook_id = $1, updated_at = now()
		WHERE singleton = TRUE
		RETURNING board_id, list_statuses, webhook_id, enabled, created_at, updated_at`, webhookID)
	settings, err := scanTrelloSettings(row)
	if err != nil {
		return trello.Settings{}, fmt.Errorf("set Trello webhook id: %w", err)
	}
	return settings, nil
}

func (s *Store) SyncTrelloCard(ctx context.Context, sync trello.CardSync) (tasks.Task, bool, error) {
	if strings.TrimSpace(sync.CardID) == "" || len(sync.CardID) > 256 || strings.TrimSpace(sync.Title) == "" || len(sync.Title) > 500 ||
		len(sync.Description) > 10000 || !tasks.ValidStatus(sync.Status) || sync.EventAt.IsZero() {
		return tasks.Task{}, false, ErrInvalidArgument
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tasks.Task{}, false, fmt.Errorf("begin Trello card sync: %w", err)
	}
	defer tx.Rollback(ctx)

	var taskID int64
	var lastEventAt time.Time
	err = tx.QueryRow(ctx, `SELECT task_id, last_event_at FROM trello_card_links WHERE card_id = $1 FOR UPDATE`, sync.CardID).Scan(&taskID, &lastEventAt)
	applied := false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		dueAt := sync.DueAt
		if sync.Status == tasks.StatusDone {
			now := time.Now().UTC()
			dueAt = &now
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO tasks (title, description, status, due_at)
			VALUES ($1, $2, $3, $4)
			RETURNING id`, sync.Title, strings.TrimSpace(sync.Description), sync.Status, dueAt).Scan(&taskID)
		if err != nil {
			return tasks.Task{}, false, fmt.Errorf("create Trello task: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO trello_card_links (card_id, task_id, last_event_at) VALUES ($1, $2, $3)`, sync.CardID, taskID, sync.EventAt); err != nil {
			return tasks.Task{}, false, fmt.Errorf("link Trello card: %w", err)
		}
		applied = true
	case err != nil:
		return tasks.Task{}, false, fmt.Errorf("get Trello card link: %w", err)
	case !sync.EventAt.After(lastEventAt):
		// Trello may redeliver an event; an older event must not overwrite newer local state.
	default:
		dueAt := sync.DueAt
		if sync.Status == tasks.StatusDone {
			now := time.Now().UTC()
			dueAt = &now
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tasks SET title = $2, description = $3, status = $4, due_at = $5, updated_at = now()
			WHERE id = $1`, taskID, sync.Title, strings.TrimSpace(sync.Description), sync.Status, dueAt); err != nil {
			return tasks.Task{}, false, fmt.Errorf("update Trello task: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE trello_card_links SET last_event_at = $2, updated_at = now() WHERE card_id = $1`, sync.CardID, sync.EventAt); err != nil {
			return tasks.Task{}, false, fmt.Errorf("update Trello card link: %w", err)
		}
		applied = true
	}
	if err := tx.Commit(ctx); err != nil {
		return tasks.Task{}, false, fmt.Errorf("commit Trello card sync: %w", err)
	}
	task, err := s.GetTask(ctx, fmt.Sprintf("%d", taskID))
	return task, applied, err
}

func scanTrelloSettings(row scannable) (trello.Settings, error) {
	var settings trello.Settings
	var listStatuses []byte
	err := row.Scan(&settings.BoardID, &listStatuses, &settings.WebhookID, &settings.Enabled, &settings.CreatedAt, &settings.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return trello.Settings{}, trello.ErrNotConfigured
	}
	if err != nil {
		return trello.Settings{}, err
	}
	if err := json.Unmarshal(listStatuses, &settings.ListStatuses); err != nil {
		return trello.Settings{}, fmt.Errorf("decode Trello list statuses: %w", err)
	}
	settings.Configured = true
	return settings, nil
}
