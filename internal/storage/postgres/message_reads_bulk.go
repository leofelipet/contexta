package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leofelipet/contexta/internal/messages"
)

// BulkAcknowledgeConfirmLimit is the number of unread messages a bulk acknowledgment
// may mark without an explicit confirmation.
const BulkAcknowledgeConfirmLimit = 5000

// unreadEligible mirrors the unread queue: audio still being transcribed is never listed or acknowledged.
const unreadEligible = `(m.type <> 'audio' OR m.transcription_status NOT IN ('pending', 'processing', 'retry'))`

// AcknowledgeMessagesBulk marks every unread message matching the filter as read by the consumer in a
// single statement. Dry runs and filters above the confirmation limit without Confirm only count.
func (s *Store) AcknowledgeMessagesBulk(ctx context.Context, params messages.BulkAcknowledgeParams) (messages.BulkAcknowledgeResult, error) {
	direction, ok := normalizeBulkDirection(params.Direction)
	if !ok || !validConsumerID(params.ConsumerID) || params.Before.IsZero() ||
		(params.After != nil && !params.After.Before(params.Before)) ||
		(params.ConversationID != "" && !isUUID(params.ConversationID)) ||
		(params.ContactID != "" && !isUUID(params.ContactID)) {
		return messages.BulkAcknowledgeResult{}, ErrInvalidArgument
	}

	result := messages.BulkAcknowledgeResult{DryRun: params.DryRun}
	var oldest, newest *time.Time
	err := s.pool.QueryRow(ctx, `
		WITH matched AS (
			SELECT m.id, m.occurred_at, receipt.message_id IS NOT NULL AS already_read
			FROM messages m
			JOIN conversations conv ON conv.id = m.conversation_id
			LEFT JOIN message_read_receipts receipt
			  ON receipt.message_id = m.id AND receipt.consumer_id = $1
			WHERE m.occurred_at < $2
			  AND ($3::timestamptz IS NULL OR m.occurred_at >= $3)
			  AND ($4::uuid IS NULL OR m.conversation_id = $4)
			  AND ($5::uuid IS NULL OR m.sender_contact_id = $5 OR conv.contact_id = $5)
			  AND ($6 = '' OR m.direction = $6)
			  AND `+unreadEligible+`
		), pending AS (
			SELECT count(*) AS total FROM matched WHERE NOT already_read
		), inserted AS (
			INSERT INTO message_read_receipts (consumer_id, message_id)
			SELECT $1, id FROM matched
			WHERE NOT already_read
			  AND NOT $7::boolean
			  AND ($8::boolean OR (SELECT total FROM pending) <= $9)
			ON CONFLICT (consumer_id, message_id) DO NOTHING
			RETURNING 1
		)
		SELECT (SELECT total FROM pending), (SELECT count(*) FROM inserted),
		       count(*) FILTER (WHERE already_read), min(occurred_at), max(occurred_at)
		FROM matched`,
		params.ConsumerID, params.Before, params.After, nullableUUID(params.ConversationID),
		nullableUUID(params.ContactID), direction, params.DryRun, params.Confirm, BulkAcknowledgeConfirmLimit,
	).Scan(&result.Matched, &result.Acknowledged, &result.AlreadyAcknowledged, &oldest, &newest)
	if err != nil {
		return messages.BulkAcknowledgeResult{}, fmt.Errorf("bulk acknowledge messages: %w", err)
	}
	result.OldestMessageAt, result.NewestMessageAt = oldest, newest
	result.ConfirmationRequired = !params.DryRun && !params.Confirm && result.Matched > BulkAcknowledgeConfirmLimit
	return result, nil
}

// CountUnreadMessages counts the consumer's unread queue grouped by calendar month in the given timezone.
func (s *Store) CountUnreadMessages(ctx context.Context, params messages.UnreadCountParams) (messages.UnreadCount, error) {
	if !validConsumerID(params.ConsumerID) || params.Timezone == "" {
		return messages.UnreadCount{}, ErrInvalidArgument
	}
	if _, err := time.LoadLocation(params.Timezone); err != nil {
		return messages.UnreadCount{}, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `
		SELECT to_char(date_trunc('month', m.occurred_at AT TIME ZONE $4), 'YYYY-MM'), count(*)
		FROM messages m
		LEFT JOIN message_read_receipts receipt
		  ON receipt.message_id = m.id AND receipt.consumer_id = $1
		WHERE receipt.message_id IS NULL
		  AND ($2::timestamptz IS NULL OR m.occurred_at < $2)
		  AND ($3::timestamptz IS NULL OR m.occurred_at >= $3)
		  AND `+unreadEligible+`
		GROUP BY 1
		ORDER BY 1`, params.ConsumerID, params.Before, params.After, params.Timezone)
	if err != nil {
		return messages.UnreadCount{}, fmt.Errorf("count unread messages: %w", err)
	}
	defer rows.Close()

	count := messages.UnreadCount{Timezone: params.Timezone, Months: []messages.UnreadMonthCount{}}
	for rows.Next() {
		var month messages.UnreadMonthCount
		if err := rows.Scan(&month.Month, &month.Count); err != nil {
			return messages.UnreadCount{}, fmt.Errorf("scan unread count: %w", err)
		}
		count.Total += month.Count
		count.Months = append(count.Months, month)
	}
	if err := rows.Err(); err != nil {
		return messages.UnreadCount{}, fmt.Errorf("iterate unread counts: %w", err)
	}
	return count, nil
}

// normalizeBulkDirection accepts inbound/outbound and their incoming/outgoing aliases; all or empty disables the filter.
func normalizeBulkDirection(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all":
		return "", true
	case "inbound", "incoming":
		return "inbound", true
	case "outbound", "outgoing":
		return "outbound", true
	}
	return "", false
}
