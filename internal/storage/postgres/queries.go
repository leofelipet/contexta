package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/contacts"
	"github.com/leofelipet/contexta/internal/conversations"
	"github.com/leofelipet/contexta/internal/messages"
	"github.com/leofelipet/contexta/internal/pagination"
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
		SELECT c.id::text, c.provider_contact_id, c.phone,
		       `+contactDisplayNameSQL("c")+` AS display_name,
		       c.push_name, c.profile_picture_url, c.created_at, c.updated_at
		FROM contacts c
		WHERE ($1 = '' OR `+contactDisplayNameSQL("c")+` ILIKE '%' || $1 || '%'
		           OR c.phone ILIKE '%' || $1 || '%'
		           OR c.name ILIKE '%' || $1 || '%'
		           OR c.push_name ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR (lower(`+contactDisplayNameSQL("c")+`), c.id) > (lower($2), $3::uuid))
		ORDER BY lower(`+contactDisplayNameSQL("c")+`), c.id
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
		SELECT c.id::text, c.provider_contact_id, c.phone,
		       `+contactDisplayNameSQL("c")+` AS display_name,
		       c.push_name, c.profile_picture_url, c.created_at, c.updated_at
		FROM contacts c WHERE c.id = $1`, id).Scan(
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

// contactDisplayNameSQL prefers saved names, then a human conversation title, never raw JIDs.
func contactDisplayNameSQL(alias string) string {
	return `COALESCE(
		NULLIF(` + alias + `.name, ''),
		NULLIF(` + alias + `.push_name, ''),
		NULLIF((
			SELECT conv.title
			FROM conversations conv
			WHERE conv.contact_id = ` + alias + `.id
			  AND conv.title <> ''
			  AND conv.title IS DISTINCT FROM ` + alias + `.phone
			  AND conv.title NOT LIKE '%@%'
			ORDER BY conv.last_message_at DESC NULLS LAST
			LIMIT 1
		), ''),
		''
	)`
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
	if params.Type != "" && params.Type != "group" && params.Type != "direct" {
		return conversations.Page{}, ErrInvalidArgument
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return conversations.Page{}, pagination.ErrInvalidCursor
	}

	rows, err := s.pool.Query(ctx, `
		SELECT c.id::text, c.provider_conversation_id, COALESCE(c.contact_id::text, ''), c.type,
		       COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''), NULLIF(cc.phone, ''), ''),
		       c.last_message_at, c.created_at, c.updated_at,
		       lm.id::text, lm.direction, lm.type, lm.display_text, lm.occurred_at,
		       COALESCE(c.last_message_at, c.created_at) AS sort_time
		FROM conversations c
		LEFT JOIN contacts cc ON cc.id = c.contact_id
		LEFT JOIN LATERAL (
			SELECT id, direction, type, COALESCE(NULLIF(text, ''), transcription_text) AS display_text, occurred_at
			FROM messages WHERE conversation_id = c.id
			ORDER BY occurred_at DESC, id DESC LIMIT 1
		) lm ON true
		WHERE ($1 = '' OR COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''), NULLIF(cc.phone, ''), '') ILIKE '%' || $1 || '%')
		  AND ($2::uuid IS NULL OR c.contact_id = $2)
		  AND ($3 = '' OR c.type = $3)
		  AND ($4::timestamptz IS NULL OR c.last_message_at >= $4)
		  AND ($5::timestamptz IS NULL OR c.last_message_at < $5)
		  AND ($6::timestamptz IS NULL OR (COALESCE(c.last_message_at, c.created_at), c.id) < ($6, $7::uuid))
		ORDER BY sort_time DESC, c.id DESC
		LIMIT $8`, params.Query, nullableUUID(params.ContactID), params.Type, params.From, params.To,
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
		var previewID, previewDirection, previewType, previewText *string
		var previewTimestamp *time.Time
		if err := rows.Scan(&current.conversation.ID, &current.conversation.ProviderConversationID,
			&current.conversation.ContactID, &current.conversation.Type, &current.conversation.Title,
			&current.conversation.LastMessageAt, &current.conversation.CreatedAt,
			&current.conversation.UpdatedAt, &previewID, &previewDirection, &previewType,
			&previewText, &previewTimestamp, &current.sortTime); err != nil {
			return conversations.Page{}, fmt.Errorf("scan conversation: %w", err)
		}
		if previewID != nil && previewTimestamp != nil {
			current.conversation.LastMessage = &conversations.Preview{
				ID: *previewID, Direction: valueOrEmpty(previewDirection), Type: valueOrEmpty(previewType),
				Text: valueOrEmpty(previewText), Timestamp: *previewTimestamp,
			}
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
	row := s.pool.QueryRow(ctx, `
		SELECT c.id::text, c.provider_conversation_id, COALESCE(c.contact_id::text, ''), c.type,
		       COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''), NULLIF(cc.phone, ''), ''),
		       c.last_message_at, c.created_at, c.updated_at,
		       lm.id::text, lm.direction, lm.type, lm.display_text, lm.occurred_at
		FROM conversations c
		LEFT JOIN contacts cc ON cc.id = c.contact_id
		LEFT JOIN LATERAL (
			SELECT id, direction, type, COALESCE(NULLIF(text, ''), transcription_text) AS display_text, occurred_at
			FROM messages WHERE conversation_id = c.id
			ORDER BY occurred_at DESC, id DESC LIMIT 1
		) lm ON true
		WHERE c.id = $1`, id)
	var previewID, previewDirection, previewType, previewText *string
	var previewTimestamp *time.Time
	err := row.Scan(
		&conversation.ID, &conversation.ProviderConversationID, &conversation.ContactID,
		&conversation.Type, &conversation.Title, &conversation.LastMessageAt,
		&conversation.CreatedAt, &conversation.UpdatedAt, &previewID, &previewDirection,
		&previewType, &previewText, &previewTimestamp,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return conversations.Conversation{}, ErrNotFound
	}
	if err != nil {
		return conversations.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	if previewID != nil && previewTimestamp != nil {
		conversation.LastMessage = &conversations.Preview{
			ID: *previewID, Direction: valueOrEmpty(previewDirection), Type: valueOrEmpty(previewType),
			Text: valueOrEmpty(previewText), Timestamp: *previewTimestamp,
		}
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
	params.ReadState = strings.ToLower(strings.TrimSpace(params.ReadState))
	if params.ConsumerID != "" && !validConsumerID(params.ConsumerID) {
		return messages.Page{}, ErrInvalidArgument
	}
	if !slices.Contains([]string{"", "all", "read", "unread"}, params.ReadState) {
		return messages.Page{}, ErrInvalidArgument
	}
	if (params.ReadState == "read" || params.ReadState == "unread") && params.ConsumerID == "" {
		return messages.Page{}, ErrInvalidArgument
	}

	rows, err := s.pool.Query(ctx, `
		SELECT m.id::text, m.provider_message_id, m.conversation_id::text,
		       COALESCE(m.sender_contact_id::text, ''), COALESCE(NULLIF(c.name, ''), NULLIF(c.push_name, ''), NULLIF(c.phone, ''), ''), m.direction,
		       m.type, m.text, m.status, m.occurred_at, COALESCE(m.reply_to_message_id::text, ''),
		       m.transcription_status, m.transcription_text, m.transcription_language,
		       m.transcription_model, m.transcribed_at, receipt.read_at
		FROM messages m
		JOIN conversations conv ON conv.id = m.conversation_id
		LEFT JOIN contacts c ON c.id = m.sender_contact_id
		LEFT JOIN message_read_receipts receipt
		  ON receipt.message_id = m.id AND receipt.consumer_id = $8
		WHERE ($1 = '' OR m.search_vector @@ websearch_to_tsquery('simple'::regconfig, $1))
		  AND ($2::timestamptz IS NULL OR m.occurred_at >= $2)
		  AND ($3::timestamptz IS NULL OR m.occurred_at < $3)
		  AND ($4::uuid IS NULL OR m.sender_contact_id = $4 OR conv.contact_id = $4)
		  AND ($5::uuid IS NULL OR m.conversation_id = $5)
		  AND ($6 = '' OR m.direction = $6)
		  AND ($7 = '' OR m.type = $7)
		  AND ($9 IN ('', 'all') OR ($9 = 'read' AND receipt.message_id IS NOT NULL) OR ($9 = 'unread' AND receipt.message_id IS NULL))
		  AND ($10::timestamptz IS NULL OR (m.occurred_at, m.id) < ($10, $11::uuid))
		ORDER BY m.occurred_at DESC, m.id DESC
		LIMIT $12`, params.Query, params.From, params.To, nullableUUID(params.ContactID),
		nullableUUID(params.ConversationID), params.Direction, params.Type,
		params.ConsumerID, params.ReadState, nullableTime(cursor.Time), nullableUUID(cursor.ID), limit+1)
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
		       COALESCE(m.sender_contact_id::text, ''), COALESCE(NULLIF(c.name, ''), NULLIF(c.push_name, ''), NULLIF(c.phone, ''), ''), m.direction,
		       m.type, m.text, m.status, m.occurred_at, COALESCE(m.reply_to_message_id::text, ''),
		       m.transcription_status, m.transcription_text, m.transcription_language,
		       m.transcription_model, m.transcribed_at, NULL::timestamptz
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
		       COALESCE(m.sender_contact_id::text, ''), COALESCE(NULLIF(c.name, ''), NULLIF(c.push_name, ''), NULLIF(c.phone, ''), ''), m.direction,
		       m.type, m.text, m.status, m.occurred_at, COALESCE(m.reply_to_message_id::text, ''),
		       m.transcription_status, m.transcription_text, m.transcription_language,
		       m.transcription_model, m.transcribed_at, NULL::timestamptz
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
	var transcriptionStatus, transcriptionText, transcriptionLanguage, transcriptionModel string
	var transcribedAt *time.Time
	if err := row.Scan(&message.ID, &message.ProviderMessageID, &message.ConversationID,
		&message.SenderContactID, &message.SenderName, &message.Direction, &message.Type,
		&message.Text, &message.Status, &message.Timestamp, &message.ReplyToMessageID,
		&transcriptionStatus, &transcriptionText, &transcriptionLanguage, &transcriptionModel,
		&transcribedAt, &message.AgentReadAt); err != nil {
		return messages.Message{}, err
	}
	if transcriptionStatus != "" {
		message.Transcription = &messages.Transcription{
			Status: transcriptionStatus, Text: transcriptionText, Language: transcriptionLanguage,
			Model: transcriptionModel, TranscribedAt: transcribedAt,
		}
	}
	return message, nil
}

func scanUnreadMessage(row rowScanner) (messages.Message, time.Time, error) {
	var message messages.Message
	var transcriptionStatus, transcriptionText, transcriptionLanguage, transcriptionModel string
	var transcribedAt *time.Time
	var createdAt time.Time
	if err := row.Scan(&message.ID, &message.ProviderMessageID, &message.ConversationID,
		&message.SenderContactID, &message.SenderName, &message.Direction, &message.Type,
		&message.Text, &message.Status, &message.Timestamp, &message.ReplyToMessageID,
		&transcriptionStatus, &transcriptionText, &transcriptionLanguage, &transcriptionModel,
		&transcribedAt, &message.AgentReadAt, &createdAt); err != nil {
		return messages.Message{}, time.Time{}, err
	}
	if transcriptionStatus != "" {
		message.Transcription = &messages.Transcription{
			Status: transcriptionStatus, Text: transcriptionText, Language: transcriptionLanguage,
			Model: transcriptionModel, TranscribedAt: transcribedAt,
		}
	}
	return message, createdAt, nil
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

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
