package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/ingestion"
)

func (s *Store) IngestMessages(ctx context.Context, batch ingestion.Batch) (ingestion.Result, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ingestion.Result{}, fmt.Errorf("begin ingestion transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	instanceID, err := ensureProviderInstance(ctx, tx, batch.Provider, batch.ProviderInstanceID)
	if err != nil {
		return ingestion.Result{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, instanceID); err != nil {
		return ingestion.Result{}, fmt.Errorf("lock provider instance: %w", err)
	}

	result := ingestion.Result{Processed: len(batch.Messages)}
	for _, message := range batch.Messages {
		if message.UpdateOnly {
			if err := updateMessage(ctx, tx, instanceID, message); err != nil {
				return ingestion.Result{}, err
			}
			result.Updated++
			continue
		}
		created, err := ingestMessage(ctx, tx, instanceID, message)
		if err != nil {
			return ingestion.Result{}, err
		}
		if created {
			result.Created++
		} else {
			result.Updated++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ingestion.Result{}, fmt.Errorf("commit ingestion transaction: %w", err)
	}
	return result, nil
}

func updateMessage(ctx context.Context, tx pgx.Tx, instanceID string, message ingestion.Message) error {
	tag, err := tx.Exec(ctx, `
		UPDATE messages SET
			status = CASE
				WHEN $3 = '' THEN status
				WHEN message_status_rank($3) >= message_status_rank(status) THEN $3
				ELSE status
			END,
			type = CASE WHEN $4 = '' THEN type ELSE $4 END,
			text = CASE WHEN $5 = '' THEN text ELSE $5 END,
			metadata = metadata || $6,
			updated_at = now()
		WHERE provider_instance_id = $1 AND provider_message_id = $2`,
		instanceID, message.ProviderMessageID, message.Status, message.Type,
		message.Text, validJSON(message.Metadata))
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		_, err := tx.Exec(ctx, `
			INSERT INTO pending_message_updates (
				provider_instance_id, provider_message_id, type, text, status, metadata
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (provider_instance_id, provider_message_id) DO UPDATE SET
				type = CASE WHEN EXCLUDED.type = '' THEN pending_message_updates.type ELSE EXCLUDED.type END,
				text = CASE WHEN EXCLUDED.text = '' THEN pending_message_updates.text ELSE EXCLUDED.text END,
				status = CASE
					WHEN message_status_rank(EXCLUDED.status) >= message_status_rank(pending_message_updates.status)
					THEN EXCLUDED.status ELSE pending_message_updates.status
				END,
				metadata = pending_message_updates.metadata || EXCLUDED.metadata,
				updated_at = now()`, instanceID, message.ProviderMessageID, message.Type,
			message.Text, message.Status, validJSON(message.Metadata))
		if err != nil {
			return fmt.Errorf("store pending message update: %w", err)
		}
	}
	return nil
}

func ensureProviderInstance(ctx context.Context, tx pgx.Tx, provider, externalID string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO provider_instances (provider, provider_instance_id)
		VALUES ($1, $2)
		ON CONFLICT (provider, provider_instance_id)
		DO UPDATE SET updated_at = now()
		RETURNING id::text`, provider, externalID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert provider instance: %w", err)
	}
	return id, nil
}

func ingestMessage(ctx context.Context, tx pgx.Tx, instanceID string, message ingestion.Message) (bool, error) {
	var conversationContactID any
	if message.Conversation.Contact != nil {
		id, err := upsertContact(ctx, tx, instanceID, *message.Conversation.Contact)
		if err != nil {
			return false, err
		}
		conversationContactID = id
	}

	var senderContactID any
	if message.Sender != nil {
		id, err := upsertContact(ctx, tx, instanceID, *message.Sender)
		if err != nil {
			return false, err
		}
		senderContactID = id
	}

	conversationMetadata := validJSON(message.Conversation.Metadata)
	var conversationID string
	err := tx.QueryRow(ctx, `
		INSERT INTO conversations (
			provider_instance_id, provider_conversation_id, contact_id, type, title,
			last_message_at, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (provider_instance_id, provider_conversation_id) DO UPDATE SET
			contact_id = COALESCE(EXCLUDED.contact_id, conversations.contact_id),
			type = CASE WHEN EXCLUDED.type = 'unknown' THEN conversations.type ELSE EXCLUDED.type END,
			title = CASE WHEN EXCLUDED.title = '' THEN conversations.title ELSE EXCLUDED.title END,
			last_message_at = GREATEST(conversations.last_message_at, EXCLUDED.last_message_at),
			metadata = conversations.metadata || EXCLUDED.metadata,
			updated_at = now()
		RETURNING id::text`, instanceID, message.Conversation.ProviderID, conversationContactID,
		normalizeConversationType(message.Conversation.Type), message.Conversation.Title,
		message.OccurredAt, conversationMetadata).Scan(&conversationID)
	if err != nil {
		return false, fmt.Errorf("upsert conversation: %w", err)
	}

	var replyToID any
	if message.ReplyToProviderMessageID != "" {
		var id string
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM messages
			WHERE provider_instance_id = $1 AND provider_message_id = $2`,
			instanceID, message.ReplyToProviderMessageID).Scan(&id)
		if err == nil {
			replyToID = id
		} else if err != pgx.ErrNoRows {
			return false, fmt.Errorf("resolve reply: %w", err)
		}
	}

	var messageID string
	var created bool
	err = tx.QueryRow(ctx, `
		INSERT INTO messages (
			provider_instance_id, provider_message_id, provider_record_id, conversation_id,
			sender_contact_id, direction, type, text, status, occurred_at,
			reply_to_message_id, reply_to_provider_message_id, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (provider_instance_id, provider_message_id) DO UPDATE SET
			provider_record_id = CASE WHEN EXCLUDED.provider_record_id = '' THEN messages.provider_record_id ELSE EXCLUDED.provider_record_id END,
			conversation_id = EXCLUDED.conversation_id,
			sender_contact_id = COALESCE(EXCLUDED.sender_contact_id, messages.sender_contact_id),
			direction = EXCLUDED.direction,
			type = CASE WHEN EXCLUDED.type = 'unknown' THEN messages.type ELSE EXCLUDED.type END,
			text = CASE WHEN EXCLUDED.text = '' THEN messages.text ELSE EXCLUDED.text END,
			status = CASE
				WHEN EXCLUDED.status = '' THEN messages.status
				WHEN message_status_rank(EXCLUDED.status) >= message_status_rank(messages.status) THEN EXCLUDED.status
				ELSE messages.status
			END,
			occurred_at = EXCLUDED.occurred_at,
			reply_to_message_id = COALESCE(EXCLUDED.reply_to_message_id, messages.reply_to_message_id),
			reply_to_provider_message_id = CASE WHEN EXCLUDED.reply_to_provider_message_id = '' THEN messages.reply_to_provider_message_id ELSE EXCLUDED.reply_to_provider_message_id END,
			metadata = messages.metadata || EXCLUDED.metadata,
			updated_at = now()
		RETURNING id::text, (xmax = 0)`, instanceID, message.ProviderMessageID, message.ProviderRecordID,
		conversationID, senderContactID, message.Direction, normalizeMessageType(message.Type),
		message.Text, message.Status, message.OccurredAt, replyToID,
		message.ReplyToProviderMessageID, validJSON(message.Metadata)).Scan(&messageID, &created)
	if err != nil {
		return false, fmt.Errorf("upsert message: %w", err)
	}
	if created && message.Audio != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcription_jobs (message_id, status)
			VALUES ($1, 'pending')
			ON CONFLICT (message_id) DO NOTHING`, messageID); err != nil {
			return false, fmt.Errorf("enqueue transcription: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE messages SET transcription_status = 'pending', updated_at = now()
			WHERE id = $1`, messageID); err != nil {
			return false, fmt.Errorf("mark transcription pending: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messages SET reply_to_message_id = $3, updated_at = now()
		WHERE provider_instance_id = $1
		  AND reply_to_provider_message_id = $2
		  AND reply_to_message_id IS NULL`, instanceID, message.ProviderMessageID, messageID); err != nil {
		return false, fmt.Errorf("backfill replies: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messages m SET
			type = CASE WHEN p.type = '' THEN m.type ELSE p.type END,
			text = CASE WHEN p.text = '' THEN m.text ELSE p.text END,
			status = CASE WHEN message_status_rank(p.status) >= message_status_rank(m.status) THEN p.status ELSE m.status END,
			metadata = m.metadata || p.metadata,
			updated_at = now()
		FROM pending_message_updates p
		WHERE p.provider_instance_id = m.provider_instance_id
		  AND p.provider_message_id = m.provider_message_id
		  AND m.id = $1`, messageID); err != nil {
		return false, fmt.Errorf("apply pending message update: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM pending_message_updates
		WHERE provider_instance_id = $1 AND provider_message_id = $2`,
		instanceID, message.ProviderMessageID); err != nil {
		return false, fmt.Errorf("delete pending message update: %w", err)
	}
	return created, nil
}

func upsertContact(ctx context.Context, tx pgx.Tx, instanceID string, contact ingestion.Contact) (string, error) {
	identities := normalizedIdentities(contact)
	var id string
	for _, identity := range identities {
		err := tx.QueryRow(ctx, `
			SELECT contact_id::text FROM contact_identities
			WHERE provider_instance_id = $1 AND identity = $2`, instanceID, identity.Value).Scan(&id)
		if err == nil {
			break
		}
		if err != pgx.ErrNoRows {
			return "", fmt.Errorf("find contact identity: %w", err)
		}
	}

	providerID := strings.TrimSpace(contact.ProviderID)
	if providerID == "" && len(identities) > 0 {
		providerID = identities[0].Value
	}
	if providerID == "" {
		return "", fmt.Errorf("contact has no provider identity")
	}

	if id == "" {
		err := tx.QueryRow(ctx, `
			INSERT INTO contacts (
				provider_instance_id, provider_contact_id, phone, name, push_name,
				profile_picture_url, metadata
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (provider_instance_id, provider_contact_id) DO UPDATE SET
				phone = CASE WHEN EXCLUDED.phone = '' THEN contacts.phone ELSE EXCLUDED.phone END,
				name = CASE WHEN EXCLUDED.name = '' THEN contacts.name ELSE EXCLUDED.name END,
				push_name = CASE WHEN EXCLUDED.push_name = '' THEN contacts.push_name ELSE EXCLUDED.push_name END,
				profile_picture_url = CASE WHEN EXCLUDED.profile_picture_url = '' THEN contacts.profile_picture_url ELSE EXCLUDED.profile_picture_url END,
				metadata = contacts.metadata || EXCLUDED.metadata,
				updated_at = now()
			RETURNING id::text`, instanceID, providerID, contact.Phone, contact.Name,
			contact.PushName, contact.ProfilePictureURL, validJSON(contact.Metadata)).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("upsert contact: %w", err)
		}
	} else {
		_, err := tx.Exec(ctx, `
			UPDATE contacts SET
				phone = CASE WHEN $2 = '' THEN phone ELSE $2 END,
				name = CASE WHEN $3 = '' THEN name ELSE $3 END,
				push_name = CASE WHEN $4 = '' THEN push_name ELSE $4 END,
				profile_picture_url = CASE WHEN $5 = '' THEN profile_picture_url ELSE $5 END,
				metadata = metadata || $6,
				updated_at = now()
			WHERE id = $1`, id, contact.Phone, contact.Name, contact.PushName,
			contact.ProfilePictureURL, validJSON(contact.Metadata))
		if err != nil {
			return "", fmt.Errorf("update contact: %w", err)
		}
	}

	for _, identity := range identities {
		_, err := tx.Exec(ctx, `
			INSERT INTO contact_identities (provider_instance_id, contact_id, kind, identity)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (provider_instance_id, identity) DO NOTHING`,
			instanceID, id, identity.Kind, identity.Value)
		if err != nil {
			return "", fmt.Errorf("upsert contact identity: %w", err)
		}
	}
	return id, nil
}

func normalizedIdentities(contact ingestion.Contact) []ingestion.Identity {
	seen := make(map[string]struct{})
	result := make([]ingestion.Identity, 0, len(contact.Identities)+1)
	all := append([]ingestion.Identity{{Kind: "provider", Value: strings.TrimSpace(contact.ProviderID)}}, contact.Identities...)
	for _, identity := range all {
		identity.Value = strings.TrimSpace(identity.Value)
		if identity.Value == "" {
			continue
		}
		if _, ok := seen[identity.Value]; ok {
			continue
		}
		seen[identity.Value] = struct{}{}
		if identity.Kind == "" {
			identity.Kind = "provider"
		}
		result = append(result, identity)
	}
	return result
}

func validJSON(value json.RawMessage) json.RawMessage {
	if !json.Valid(value) {
		return json.RawMessage(`{}`)
	}
	return value
}

func normalizeConversationType(value string) string {
	switch value {
	case "direct", "group", "newsletter":
		return value
	default:
		return "unknown"
	}
}

func normalizeMessageType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
