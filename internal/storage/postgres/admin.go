package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/admin"
	"github.com/leofelipet/contexta/internal/pagination"
)

func (s *Store) Dashboard(ctx context.Context) (admin.Dashboard, error) {
	var dashboard admin.Dashboard
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM contacts),
			(SELECT count(*) FROM conversations),
			(SELECT count(*) FROM messages),
			(SELECT max(occurred_at) FROM messages),
			(SELECT max(occurred_at) FROM activity_events WHERE category = 'webhook')`).Scan(
		&dashboard.Contacts, &dashboard.Conversations, &dashboard.Messages,
		&dashboard.LastMessageAt, &dashboard.LastWebhookAt,
	)
	if err != nil {
		return admin.Dashboard{}, fmt.Errorf("load dashboard: %w", err)
	}
	return dashboard, nil
}

func (s *Store) LastActivityAt(ctx context.Context, category string) (*time.Time, error) {
	var timestamp *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM activity_events WHERE category = $1`, category).Scan(&timestamp); err != nil {
		return nil, fmt.Errorf("load last activity: %w", err)
	}
	return timestamp, nil
}

func (s *Store) RecordActivity(ctx context.Context, record activity.Record) error {
	if record.Category == "" || record.Operation == "" || record.Outcome == "" {
		return errors.New("activity category, operation and outcome are required")
	}
	if !slices.Contains([]string{"info", "warning", "error"}, record.Level) {
		return errors.New("activity level must be info, warning or error")
	}
	metadata := record.Metadata
	if !json.Valid(metadata) {
		metadata = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO activity_events (
			category, level, operation, outcome, entity_type, entity_id, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		record.Category, record.Level, record.Operation, record.Outcome,
		record.EntityType, record.EntityID, metadata)
	if err != nil {
		return fmt.Errorf("record activity: %w", err)
	}
	return nil
}

func (s *Store) ListActivity(ctx context.Context, params activity.ListParams) (activity.Page, error) {
	if params.Level != "" && !slices.Contains([]string{"info", "warning", "error"}, params.Level) {
		return activity.Page{}, ErrInvalidArgument
	}
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "activity")
	if err != nil {
		return activity.Page{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return activity.Page{}, pagination.ErrInvalidCursor
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, category, level, operation, outcome, entity_type,
		       entity_id, metadata, occurred_at
		FROM activity_events
		WHERE ($1 = '' OR category = $1)
		  AND ($2 = '' OR level = $2)
		  AND ($3::timestamptz IS NULL OR occurred_at >= $3)
		  AND ($4::timestamptz IS NULL OR occurred_at < $4)
		  AND ($5::timestamptz IS NULL OR (occurred_at, id) < ($5, $6::uuid))
		ORDER BY occurred_at DESC, id DESC
		LIMIT $7`, params.Category, params.Level, params.From, params.To,
		nullableTime(cursor.Time), nullableUUID(cursor.ID), limit+1)
	if err != nil {
		return activity.Page{}, fmt.Errorf("list activity: %w", err)
	}
	defer rows.Close()

	events := make([]activity.Event, 0, limit+1)
	for rows.Next() {
		var event activity.Event
		if err := rows.Scan(&event.ID, &event.Category, &event.Level, &event.Operation,
			&event.Outcome, &event.EntityType, &event.EntityID, &event.Metadata,
			&event.OccurredAt); err != nil {
			return activity.Page{}, fmt.Errorf("scan activity: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return activity.Page{}, fmt.Errorf("iterate activity: %w", err)
	}
	page := activity.Page{Events: events}
	if len(events) > limit {
		last := events[limit-1]
		page.Events = events[:limit]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "activity", Time: last.OccurredAt, ID: last.ID})
	}
	return page, nil
}
