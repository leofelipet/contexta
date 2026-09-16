package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/leofelipet/contexta/internal/messages"
	"github.com/leofelipet/contexta/internal/pagination"
)

const maxAcknowledgeMessages = 100

func (s *Store) ListUnreadMessages(ctx context.Context, params messages.UnreadParams) (messages.Page, error) {
	if !validConsumerID(params.ConsumerID) {
		return messages.Page{}, ErrInvalidArgument
	}
	if params.ConversationID != "" && !isUUID(params.ConversationID) {
		return messages.Page{}, ErrInvalidArgument
	}
	if params.Direction != "" && params.Direction != "inbound" && params.Direction != "outbound" {
		return messages.Page{}, errors.New("direction must be inbound or outbound")
	}
	order := strings.ToLower(strings.TrimSpace(params.Order))
	if order == "" {
		order = "newest"
	}
	if order != "newest" && order != "oldest" {
		return messages.Page{}, ErrInvalidArgument
	}
	kind := "unread_messages_" + order
	cursor, err := pagination.Decode(params.Cursor, kind)
	if err != nil {
		return messages.Page{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return messages.Page{}, pagination.ErrInvalidCursor
	}

	comparison, direction := "<", "DESC"
	if order == "oldest" {
		comparison, direction = ">", "ASC"
	}
	limit := normalizeLimit(params.Limit, 20)
	query := fmt.Sprintf(`
		SELECT m.id::text, m.provider_message_id, m.conversation_id::text,
		       COALESCE(m.sender_contact_id::text, ''), COALESCE(c.name, ''), m.direction,
		       m.type, m.text, m.status, m.occurred_at, COALESCE(m.reply_to_message_id::text, ''),
		       m.transcription_status, m.transcription_text, m.transcription_language,
		       m.transcription_model, m.transcribed_at, receipt.read_at, m.created_at
		FROM messages m
		LEFT JOIN contacts c ON c.id = m.sender_contact_id
		LEFT JOIN message_read_receipts receipt
		  ON receipt.message_id = m.id AND receipt.consumer_id = $1
		WHERE receipt.message_id IS NULL
		  AND ($2::uuid IS NULL OR m.conversation_id = $2)
		  AND ($3 = '' OR m.direction = $3)
		  AND ($4 = '' OR m.type = $4)
		  AND (m.type <> 'audio' OR m.transcription_status NOT IN ('pending', 'processing', 'retry'))
		  AND ($5::timestamptz IS NULL OR (m.created_at, m.id) %s ($5, $6::uuid))
		ORDER BY m.created_at %s, m.id %s
		LIMIT $7`, comparison, direction, direction)
	rows, err := s.pool.Query(ctx, query, params.ConsumerID, nullableUUID(params.ConversationID),
		params.Direction, params.Type, nullableTime(cursor.Time), nullableUUID(cursor.ID), limit+1)
	if err != nil {
		return messages.Page{}, fmt.Errorf("list unread messages: %w", err)
	}
	defer rows.Close()

	type item struct {
		message   messages.Message
		createdAt time.Time
	}
	items := make([]item, 0, limit+1)
	for rows.Next() {
		message, createdAt, err := scanUnreadMessage(rows)
		if err != nil {
			return messages.Page{}, fmt.Errorf("scan unread message: %w", err)
		}
		items = append(items, item{message: message, createdAt: createdAt})
	}
	if err := rows.Err(); err != nil {
		return messages.Page{}, fmt.Errorf("iterate unread messages: %w", err)
	}

	page := messages.Page{Messages: make([]messages.Message, 0, min(limit, len(items)))}
	for _, item := range items[:min(limit, len(items))] {
		page.Messages = append(page.Messages, item.message)
	}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: kind, Time: last.createdAt, ID: last.message.ID})
	}
	return page, nil
}

func (s *Store) AcknowledgeMessages(ctx context.Context, consumerID string, messageIDs []string) (int, error) {
	if !validConsumerID(consumerID) || len(messageIDs) == 0 || len(messageIDs) > maxAcknowledgeMessages {
		return 0, ErrInvalidArgument
	}
	unique := make([]string, 0, len(messageIDs))
	seen := make(map[string]struct{}, len(messageIDs))
	for _, id := range messageIDs {
		if !isUUID(id) {
			return 0, ErrInvalidArgument
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin message acknowledgment: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO message_read_receipts (consumer_id, message_id)
		SELECT $1, m.id
		FROM messages m
		WHERE m.id = ANY($2::uuid[])
		  AND (m.type <> 'audio' OR m.transcription_status NOT IN ('pending', 'processing', 'retry'))
		ON CONFLICT (consumer_id, message_id) DO NOTHING`, consumerID, unique); err != nil {
		return 0, fmt.Errorf("acknowledge messages: %w", err)
	}
	var acknowledged int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM message_read_receipts receipt
		JOIN messages m ON m.id = receipt.message_id
		WHERE receipt.consumer_id = $1
		  AND receipt.message_id = ANY($2::uuid[])
		  AND (m.type <> 'audio' OR m.transcription_status NOT IN ('pending', 'processing', 'retry'))`,
		consumerID, unique).Scan(&acknowledged); err != nil {
		return 0, fmt.Errorf("count acknowledged messages: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit message acknowledgment: %w", err)
	}
	return acknowledged, nil
}

func validConsumerID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._:-", char) {
			continue
		}
		return false
	}
	return true
}
