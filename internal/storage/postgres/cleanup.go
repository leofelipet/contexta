package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/leofelipet/contexta/internal/conversations"
	"github.com/leofelipet/contexta/internal/pagination"
)

func (s *Store) ListStaleConversations(ctx context.Context, params conversations.StaleParams) (conversations.StalePage, error) {
	days := params.Days
	if days <= 0 {
		days = 30
	}
	if days > 3650 {
		days = 3650
	}
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "stale_conversations")
	if err != nil {
		return conversations.StalePage{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return conversations.StalePage{}, pagination.ErrInvalidCursor
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	rows, err := s.pool.Query(ctx, `
		SELECT c.id::text, c.type,
		       COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''), NULLIF(cc.phone, ''), ''),
		       c.last_message_at, c.created_at,
		       (SELECT count(*)::bigint FROM messages m WHERE m.conversation_id = c.id) AS message_count,
		       COALESCE(c.last_message_at, c.created_at) AS activity_at
		FROM conversations c
		LEFT JOIN contacts cc ON cc.id = c.contact_id
		WHERE COALESCE(c.last_message_at, c.created_at) < $1
		  AND ($2::timestamptz IS NULL OR (COALESCE(c.last_message_at, c.created_at), c.id) < ($2, $3::uuid))
		ORDER BY activity_at ASC, c.id ASC
		LIMIT $4`, cutoff, nullableTime(cursor.Time), nullableUUID(cursor.ID), limit+1)
	if err != nil {
		return conversations.StalePage{}, fmt.Errorf("list stale conversations: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	items := make([]conversations.StaleConversation, 0, limit+1)
	for rows.Next() {
		var item conversations.StaleConversation
		var activityAt time.Time
		if err := rows.Scan(&item.ID, &item.Type, &item.Title, &item.LastMessageAt, &item.CreatedAt, &item.MessageCount, &activityAt); err != nil {
			return conversations.StalePage{}, fmt.Errorf("scan stale conversation: %w", err)
		}
		item.InactiveDays = int(now.Sub(activityAt).Hours() / 24)
		if item.InactiveDays < days {
			item.InactiveDays = days
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return conversations.StalePage{}, fmt.Errorf("iterate stale conversations: %w", err)
	}

	page := conversations.StalePage{Conversations: items[:min(limit, len(items))], Days: days}
	if len(items) > limit {
		last := items[limit-1]
		activity := last.CreatedAt
		if last.LastMessageAt != nil {
			activity = *last.LastMessageAt
		}
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "stale_conversations", Time: activity, ID: last.ID})
	}
	return page, nil
}

func (s *Store) DeleteConversation(ctx context.Context, id string) (conversations.DeleteResult, error) {
	if !isUUID(id) {
		return conversations.DeleteResult{}, ErrInvalidArgument
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return conversations.DeleteResult{}, fmt.Errorf("begin delete conversation: %w", err)
	}
	defer tx.Rollback(ctx)

	var messageCount int64
	err = tx.QueryRow(ctx, `SELECT count(*)::bigint FROM messages WHERE conversation_id = $1`, id).Scan(&messageCount)
	if err != nil {
		return conversations.DeleteResult{}, fmt.Errorf("count conversation messages: %w", err)
	}

	tag, err := tx.Exec(ctx, `DELETE FROM conversations WHERE id = $1`, id)
	if err != nil {
		return conversations.DeleteResult{}, fmt.Errorf("delete conversation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return conversations.DeleteResult{}, ErrNotFound
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM denylist_entries
		WHERE target_type = 'conversation' AND target_id = $1`, id); err != nil {
		return conversations.DeleteResult{}, fmt.Errorf("cleanup denylist entry: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return conversations.DeleteResult{}, fmt.Errorf("commit delete conversation: %w", err)
	}
	return conversations.DeleteResult{Deleted: true, MessageCount: messageCount}, nil
}
