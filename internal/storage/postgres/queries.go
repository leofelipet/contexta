package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipe/contexta/internal/contacts"
	"github.com/leofelipe/contexta/internal/conversations"
	"github.com/leofelipe/contexta/internal/messages"
	"github.com/leofelipe/contexta/internal/pagination"
)

var ErrNotFound = errors.New("not found")
var ErrInvalidArgument = errors.New("invalid argument")

const maxPageSize = 200

func (s *Store) ListContacts(ctx context.Context, params contacts.ListParams) (contacts.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "contacts")
	if err != nil {
		return contacts.Page{}, err
	}
	if params.Cursor != "" && !isUUID(cursor.ID) {
		return contacts.Page{}, pagination.ErrInvalidCursor
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id::text, provider_contact_id, phone, name, push_name,
		       profile_picture_url, created_at, updated_at
		FROM contacts
		WHERE ($1 = '' OR name ILIKE '%' || $1 || '%' OR phone ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR (lower(name), id) > (lower($2), $3::uuid))
		ORDER BY lower(name), id
		LIMIT $4`, params.Query, cursor.Text, nullableUUID(cursor.ID), limit+1)
	if err != nil {
		return contacts.Page{}, fmt.Errorf("list contacts: %w", err)
	}
	defer rows.Close()

	result := make([]contacts.Contact, 0, limit+1)
	for rows.Next() {
		var contact contacts.Contact
		if err := rows.Scan(&contact.ID, &contact.ProviderContactID, &contact.Phone, &contact.Name,
			&contact.PushName, &contact.ProfilePictureURL, &contact.CreatedAt, &contact.UpdatedAt); err != nil {
			return contacts.Page{}, fmt.Errorf("scan contact: %w", err)
		}
		result = append(result, contact)
	}
	if err := rows.Err(); err != nil {
		return contacts.Page{}, fmt.Errorf("iterate contacts: %w", err)
	}

	page := contacts.Page{Contacts: result}
	if len(result) > limit {
		last := result[limit-1]
		page.Contacts = result[:limit]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "contacts", Text: last.Name, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetContact(ctx context.Context, id string) (contacts.Contact, error) {
	if !isUUID(id) {
		return contacts.Contact{}, ErrInvalidArgument
	}
	var contact contacts.Contact
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, provider_contact_id, phone, name, push_name,
		       profile_picture_url, created_at, updated_at
		FROM contacts WHERE id = $1`, id).Scan(
		&contact.ID, &contact.ProviderContactID, &contact.Phone, &contact.Name,
		&contact.PushName, &contact.ProfilePictureURL, &contact.CreatedAt, &contact.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return contacts.Contact{}, ErrNotFound
	}
	if err != nil {
		return contacts.Contact{}, fmt.Errorf("get contact: %w", err)
	}
	return contact, nil
}

func (s *Store) ListConversations(ctx context.Context, params conversations.ListParams) (conversations.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "conversations")
	if err != nil {
		return conversations.Page{}, err
	}
	if params.ContactID != "" && !isUUID(params.ContactID) {
		return conversations.Page{}, ErrInvalidArgument
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return conversations.Page{}, pagination.ErrInvalidCursor
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id::text, provider_conversation_id, COALESCE(contact_id::text, ''), type,
		       title, last_message_at, created_at, updated_at,
		       COALESCE(last_message_at, created_at) AS sort_time
		FROM conversations
		WHERE ($1 = '' OR title ILIKE '%' || $1 || '%' OR provider_conversation_id ILIKE '%' || $1 || '%')
		  AND ($2::uuid IS NULL OR contact_id = $2)
		  AND ($3::timestamptz IS NULL OR last_message_at >= $3)
		  AND ($4::timestamptz IS NULL OR last_message_at < $4)
		  AND ($5::timestamptz IS NULL OR (COALESCE(last_message_at, created_at), id) < ($5, $6::uuid))
		ORDER BY sort_time DESC, id DESC
		LIMIT $7`, params.Query, nullableUUID(params.ContactID), params.From, params.To,
		nullableTime(cursor.Time), nullableUUID(cursor.ID), limit+1)
	if err != nil {
		return conversations.Page{}, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	type item struct {
		conversation conversations.Conversation
		sortTime     time.Time
	}
	result := make([]item, 0, limit+1)
	for rows.Next() {
		var current item
		if err := rows.Scan(&current.conversation.ID, &current.conversation.ProviderConversationID,
			&current.conversation.ContactID, &current.conversation.Type, &current.conversation.Title,
			&current.conversation.LastMessageAt, &current.conversation.CreatedAt,
			&current.conversation.UpdatedAt, &current.sortTime); err != nil {
			return conversations.Page{}, fmt.Errorf("scan conversation: %w", err)
		}
		result = append(result, current)
	}
	if err := rows.Err(); err != nil {
		return conversations.Page{}, fmt.Errorf("iterate conversations: %w", err)
	}

	page := conversations.Page{Conversations: make([]conversations.Conversation, 0, min(limit, len(result)))}
	for _, current := range result[:min(limit, len(result))] {
		page.Conversations = append(page.Conversations, current.conversation)
	}
	if len(result) > limit {
		last := result[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "conversations", Time: last.sortTime, ID: last.conversation.ID})
	}
	return page, nil
}

func (s *Store) GetConversation(ctx context.Context, id string) (conversations.Conversation, error) {
	if !isUUID(id) {
		return conversations.Conversation{}, ErrInvalidArgument
	}
	var conversation conversations.Conversation
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, provider_conversation_id, COALESCE(contact_id::text, ''), type,
		       title, last_message_at, created_at, updated_at
		FROM conversations WHERE id = $1`, id).Scan(
		&conversation.ID, &conversation.ProviderConversationID, &conversation.ContactID,
		&conversation.Type, &conversation.Title, &conversation.LastMessageAt,
		&conversation.CreatedAt, &conversation.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return conversations.Conversation{}, ErrNotFound
	}
	if err != nil {
		return conversations.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return conversation, nil
}

func (s *Store) SearchMessages(ctx context.Context, params messages.SearchParams) (messages.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "messages")
	if err != nil {
		return messages.Page{}, err
	}
	if params.ContactID != "" && !isUUID(params.ContactID) {
		return messages.Page{}, ErrInvalidArgument
	}
	if params.ConversationID != "" && !isUUID(params.ConversationID) {
		return messages.Page{}, ErrInvalidArgument
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return messages.Page{}, pagination.ErrInvalidCursor
	}
	if params.Direction != "" && !slices.Contains([]string{"inbound", "outbound"}, params.Direction) {
		return messages.Page{}, errors.New("direction must be inbound or outbound")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT m.id::text, m.provider_message_id, m.conversation_id::text,
		       COALESCE(m.sender_contact_id::text, ''), COALESCE(c.name, ''), m.direction,
		       m.type, m.text, m.status, m.occurred_at, COALESCE(m.reply_to_message_id::text, '')
		FROM messages m
		JOIN conversations conv ON conv.id = m.conversation_id
		LEFT JOIN contacts c ON c.id = m.sender_contact_id
		WHERE ($1 = '' OR m.search_vector @@ websearch_to_tsquery('simple'::regconfig, $1))
		  AND ($2::timestamptz IS NULL OR m.occurred_at >= $2)
		  AND ($3::timestamptz IS NULL OR m.occurred_at < $3)
		  AND ($4::uuid IS NULL OR m.sender_contact_id = $4 OR conv.contact_id = $4)
		  AND ($5::uuid IS NULL OR m.conversation_id = $5)
		  AND ($6 = '' OR m.direction = $6)
		  AND ($7::timestamptz IS NULL OR (m.occurred_at, m.id) < ($7, $8::uuid))
		ORDER BY m.occurred_at DESC, m.id DESC
		LIMIT $9`, params.Query, params.From, params.To, nullableUUID(params.ContactID),
		nullableUUID(params.ConversationID), params.Direction, nullableTime(cursor.Time), nullableUUID(cursor.ID), limit+1)
	if err != nil {
		return messages.Page{}, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	result := make([]messages.Message, 0, limit+1)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return messages.Page{}, err
		}
		result = append(result, message)
	}
	if err := rows.Err(); err != nil {
		return messages.Page{}, fmt.Errorf("iterate messages: %w", err)
	}

	page := messages.Page{Messages: result}
	if len(result) > limit {
		last := result[limit-1]
		page.Messages = result[:limit]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "messages", Time: last.Timestamp, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetMessage(ctx context.Context, id string) (messages.Message, error) {
	if !isUUID(id) {
		return messages.Message{}, ErrInvalidArgument
	}
	message, err := scanMessage(s.pool.QueryRow(ctx, `
		SELECT m.id::text, m.provider_message_id, m.conversation_id::text,
		       COALESCE(m.sender_contact_id::text, ''), COALESCE(c.name, ''), m.direction,
		       m.type, m.text, m.status, m.occurred_at, COALESCE(m.reply_to_message_id::text, '')
		FROM messages m LEFT JOIN contacts c ON c.id = m.sender_contact_id
		WHERE m.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return messages.Message{}, ErrNotFound
	}
	if err != nil {
		return messages.Message{}, fmt.Errorf("get message: %w", err)
	}
	return message, nil
}

func (s *Store) GetMessagesAround(ctx context.Context, id string, before, after int) (messages.Around, error) {
	anchor, err := s.GetMessage(ctx, id)
	if err != nil {
		return messages.Around{}, err
	}
	before = normalizeAroundLimit(before)
	after = normalizeAroundLimit(after)

	previous, err := s.messagesRelative(ctx, anchor, "<", "DESC", before)
	if err != nil {
		return messages.Around{}, err
	}
	slices.Reverse(previous)
	next, err := s.messagesRelative(ctx, anchor, ">", "ASC", after)
	if err != nil {
		return messages.Around{}, err
	}
	return messages.Around{Previous: previous, Message: anchor, Next: next}, nil
}

func (s *Store) messagesRelative(ctx context.Context, anchor messages.Message, operator, order string, limit int) ([]messages.Message, error) {
	if operator != "<" && operator != ">" {
		return nil, errors.New("invalid relative operator")
	}
	query := fmt.Sprintf(`
		SELECT m.id::text, m.provider_message_id, m.conversation_id::text,
		       COALESCE(m.sender_contact_id::text, ''), COALESCE(c.name, ''), m.direction,
		       m.type, m.text, m.status, m.occurred_at, COALESCE(m.reply_to_message_id::text, '')
		FROM messages m LEFT JOIN contacts c ON c.id = m.sender_contact_id
		WHERE m.conversation_id = $1 AND (m.occurred_at, m.id) %s ($2, $3::uuid)
		ORDER BY m.occurred_at %s, m.id %s LIMIT $4`, operator, order, order)
	rows, err := s.pool.Query(ctx, query, anchor.ConversationID, anchor.Timestamp, anchor.ID, limit)
	if err != nil {
		return nil, fmt.Errorf("get relative messages: %w", err)
	}
	defer rows.Close()
	result := make([]messages.Message, 0, limit)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	return result, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMessage(row rowScanner) (messages.Message, error) {
	var message messages.Message
	if err := row.Scan(&message.ID, &message.ProviderMessageID, &message.ConversationID,
		&message.SenderContactID, &message.SenderName, &message.Direction, &message.Type,
		&message.Text, &message.Status, &message.Timestamp, &message.ReplyToMessageID); err != nil {
		return messages.Message{}, err
	}
	return message, nil
}

func normalizeLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return min(value, maxPageSize)
}

func normalizeAroundLimit(value int) int {
	if value <= 0 {
		return 10
	}
	return min(value, 50)
}

func nullableUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
