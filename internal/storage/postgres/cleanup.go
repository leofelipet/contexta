package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/leofelipet/contexta/internal/conversations"
	"github.com/leofelipet/contexta/internal/pagination"
)

const blockedCleanupLimit = 200

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

	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -days)
	page := conversations.StalePage{Days: days, Conversations: make([]conversations.StaleConversation, 0, limit)}

	if params.Cursor == "" {
		blocked, err := s.queryCleanupConversations(ctx, cleanupQuery{
			blockedOnly: true,
			limit:       blockedCleanupLimit,
			now:         now,
		})
		if err != nil {
			return conversations.StalePage{}, err
		}
		page.Conversations = append(page.Conversations, blocked...)
	}

	stale, err := s.queryCleanupConversations(ctx, cleanupQuery{
		blockedOnly: false,
		cutoff:      &cutoff,
		cursorTime:  nullableTime(cursor.Time),
		cursorID:    nullableUUID(cursor.ID),
		limit:       limit + 1,
		now:         now,
	})
	if err != nil {
		return conversations.StalePage{}, err
	}
	if len(stale) > limit {
		last := stale[limit-1]
		activity := last.CreatedAt
		if last.LastMessageAt != nil {
			activity = *last.LastMessageAt
		}
		page.NextCursor = pagination.Encode(pagination.Cursor{
			Kind: "stale_conversations", Time: activity, ID: last.ID,
		})
		stale = stale[:limit]
	}
	page.Conversations = append(page.Conversations, stale...)
	return page, nil
}

type cleanupQuery struct {
	blockedOnly bool
	cutoff      *time.Time
	cursorTime  any
	cursorID    any
	limit       int
	now         time.Time
}

func (s *Store) queryCleanupConversations(ctx context.Context, params cleanupQuery) ([]conversations.StaleConversation, error) {
	if params.limit <= 0 {
		return nil, nil
	}

	blockedPredicate := `
		EXISTS (
		  SELECT 1 FROM denylist_entries d
		  WHERE (d.target_type = 'conversation' AND d.target_id = c.id)
		     OR (d.target_type = 'contact' AND c.contact_id IS NOT NULL AND d.target_id = c.contact_id)
		)`

	var query string
	var args []any
	if params.blockedOnly {
		query = `
			SELECT c.id::text, c.type,
			       COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''), NULLIF(cc.phone, ''), ''),
			       c.last_message_at, c.created_at,
			       (SELECT count(*)::bigint FROM messages m WHERE m.conversation_id = c.id) AS message_count,
			       COALESCE(c.last_message_at, c.created_at) AS activity_at
			FROM conversations c
			LEFT JOIN contacts cc ON cc.id = c.contact_id
			WHERE ` + blockedPredicate + `
			ORDER BY activity_at ASC, c.id ASC
			LIMIT $1`
		args = []any{params.limit}
	} else {
		query = `
			SELECT c.id::text, c.type,
			       COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''), NULLIF(cc.phone, ''), ''),
			       c.last_message_at, c.created_at,
			       (SELECT count(*)::bigint FROM messages m WHERE m.conversation_id = c.id) AS message_count,
			       COALESCE(c.last_message_at, c.created_at) AS activity_at
			FROM conversations c
			LEFT JOIN contacts cc ON cc.id = c.contact_id
			WHERE COALESCE(c.last_message_at, c.created_at) < $1
			  AND NOT (` + blockedPredicate + `)
			  AND ($2::timestamptz IS NULL OR (COALESCE(c.last_message_at, c.created_at), c.id) < ($2, $3::uuid))
			ORDER BY activity_at ASC, c.id ASC
			LIMIT $4`
		args = []any{params.cutoff, params.cursorTime, params.cursorID, params.limit}
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list cleanup conversations: %w", err)
	}
	defer rows.Close()

	items := make([]conversations.StaleConversation, 0, params.limit)
	for rows.Next() {
		var item conversations.StaleConversation
		var activityAt time.Time
		if err := rows.Scan(&item.ID, &item.Type, &item.Title, &item.LastMessageAt, &item.CreatedAt, &item.MessageCount, &activityAt); err != nil {
			return nil, fmt.Errorf("scan cleanup conversation: %w", err)
		}
		item.Blocked = params.blockedOnly
		item.InactiveDays = int(params.now.Sub(activityAt).Hours() / 24)
		if item.InactiveDays < 0 {
			item.InactiveDays = 0
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cleanup conversations: %w", err)
	}
	return items, nil
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
