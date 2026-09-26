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
			(SELECT count(*) FROM messages WHERE direction = 'inbound'),
			(SELECT count(*) FROM messages WHERE direction = 'outbound'),
			(SELECT count(*) FROM messages WHERE occurred_at >= now() - interval '7 days'),
			(SELECT count(*) FROM messages WHERE occurred_at >= now() - interval '30 days'),
			(SELECT count(*) FROM conversations WHERE type = 'group'),
			(SELECT count(*) FROM conversations WHERE type = 'direct'),
			(SELECT count(DISTINCT conversation_id) FROM messages WHERE occurred_at >= now() - interval '7 days'),
			(SELECT max(occurred_at) FROM messages),
			(SELECT max(occurred_at) FROM activity_events WHERE category = 'webhook')`).Scan(
		&dashboard.Contacts, &dashboard.Conversations, &dashboard.Messages,
		&dashboard.MessagesInbound, &dashboard.MessagesOutbound,
		&dashboard.MessagesLast7D, &dashboard.MessagesLast30D,
		&dashboard.Groups, &dashboard.Directs, &dashboard.ActiveConversations7D,
		&dashboard.LastMessageAt, &dashboard.LastWebhookAt,
	)
	if err != nil {
		return admin.Dashboard{}, fmt.Errorf("load dashboard: %w", err)
	}

	traffic, err := s.dashboardTraffic(ctx, 14)
	if err != nil {
		return admin.Dashboard{}, err
	}
	dashboard.Traffic = traffic

	types, err := s.dashboardMessageTypes(ctx, 30)
	if err != nil {
		return admin.Dashboard{}, err
	}
	dashboard.MessageTypes = types

	top, err := s.dashboardTopConversations(ctx, 30, 8)
	if err != nil {
		return admin.Dashboard{}, err
	}
	dashboard.TopConversations = top

	return dashboard, nil
}

func (s *Store) dashboardTraffic(ctx context.Context, days int) ([]admin.DailyTraffic, error) {
	if days <= 0 {
		days = 14
	}
	rows, err := s.pool.Query(ctx, `
		WITH days AS (
			SELECT generate_series(
				(CURRENT_DATE - ($1::int - 1) * INTERVAL '1 day')::date,
				CURRENT_DATE,
				INTERVAL '1 day'
			)::date AS day
		),
		counts AS (
			SELECT
				(occurred_at AT TIME ZONE 'UTC')::date AS day,
				count(*) FILTER (WHERE direction = 'inbound') AS inbound,
				count(*) FILTER (WHERE direction = 'outbound') AS outbound
			FROM messages
			WHERE occurred_at >= CURRENT_DATE - ($1::int - 1) * INTERVAL '1 day'
			GROUP BY 1
		)
		SELECT d.day::text,
		       COALESCE(c.inbound, 0),
		       COALESCE(c.outbound, 0)
		FROM days d
		LEFT JOIN counts c ON c.day = d.day
		ORDER BY d.day`, days)
	if err != nil {
		return nil, fmt.Errorf("load dashboard traffic: %w", err)
	}
	defer rows.Close()

	items := make([]admin.DailyTraffic, 0, days)
	for rows.Next() {
		var item admin.DailyTraffic
		if err := rows.Scan(&item.Date, &item.Inbound, &item.Outbound); err != nil {
			return nil, fmt.Errorf("scan dashboard traffic: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dashboard traffic: %w", err)
	}
	return items, nil
}

func (s *Store) dashboardMessageTypes(ctx context.Context, days int) ([]admin.NamedCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(type, ''), 'unknown') AS name, count(*)::bigint
		FROM messages
		WHERE occurred_at >= now() - ($1::int * INTERVAL '1 day')
		GROUP BY 1
		ORDER BY 2 DESC
		LIMIT 8`, days)
	if err != nil {
		return nil, fmt.Errorf("load dashboard message types: %w", err)
	}
	defer rows.Close()

	items := make([]admin.NamedCount, 0, 8)
	for rows.Next() {
		var item admin.NamedCount
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			return nil, fmt.Errorf("scan dashboard message types: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dashboard message types: %w", err)
	}
	return items, nil
}

func (s *Store) dashboardTopConversations(ctx context.Context, days, limit int) ([]admin.TopConversation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id::text,
		       COALESCE(NULLIF(c.title, ''), NULLIF(ct.name, ''), NULLIF(ct.push_name, ''), NULLIF(ct.phone, ''), 'Conversa sem título'),
		       c.type,
		       count(m.id)::bigint AS message_count
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		LEFT JOIN contacts ct ON ct.id = c.contact_id
		WHERE m.occurred_at >= now() - ($1::int * INTERVAL '1 day')
		GROUP BY c.id, c.title, c.type, ct.name, ct.push_name, ct.phone
		ORDER BY message_count DESC
		LIMIT $2`, days, limit)
	if err != nil {
		return nil, fmt.Errorf("load dashboard top conversations: %w", err)
	}
	defer rows.Close()

	items := make([]admin.TopConversation, 0, limit)
	for rows.Next() {
		var item admin.TopConversation
		if err := rows.Scan(&item.ID, &item.Title, &item.Type, &item.MessageCount); err != nil {
			return nil, fmt.Errorf("scan dashboard top conversations: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dashboard top conversations: %w", err)
	}
	return items, nil
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
