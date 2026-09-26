package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/denylist"
	"github.com/leofelipet/contexta/internal/pagination"
)

func (s *Store) ListDenylist(ctx context.Context, params denylist.ListParams) (denylist.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "denylist")
	if err != nil {
		return denylist.Page{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return denylist.Page{}, pagination.ErrInvalidCursor
	}

	rows, err := s.pool.Query(ctx, `
		SELECT d.id::text, d.target_type, d.target_id::text, COALESCE(d.reason, ''), d.created_at,
		       CASE
		         WHEN d.target_type = 'conversation' THEN COALESCE(
		           NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''),
		           NULLIF(cc.phone, ''), NULLIF(c.provider_conversation_id, ''), ''
		         )
		         WHEN d.target_type = 'contact' THEN COALESCE(
		           NULLIF(ct.name, ''), NULLIF(ct.push_name, ''), NULLIF(ct.phone, ''),
		           NULLIF(ct.provider_contact_id, ''), ''
		         )
		         ELSE ''
		       END AS target_label
		FROM denylist_entries d
		LEFT JOIN conversations c ON d.target_type = 'conversation' AND c.id = d.target_id
		LEFT JOIN contacts cc ON c.contact_id = cc.id
		LEFT JOIN contacts ct ON d.target_type = 'contact' AND ct.id = d.target_id
		WHERE ($1::timestamptz IS NULL OR (d.created_at, d.id) < ($1, $2::uuid))
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT $3`, nullableTime(cursor.Time), nullableUUID(cursor.ID), limit+1)
	if err != nil {
		return denylist.Page{}, fmt.Errorf("list denylist: %w", err)
	}
	defer rows.Close()

	entries := make([]denylist.Entry, 0, limit+1)
	for rows.Next() {
		var entry denylist.Entry
		if err := rows.Scan(&entry.ID, &entry.TargetType, &entry.TargetID, &entry.Reason, &entry.CreatedAt, &entry.TargetLabel); err != nil {
			return denylist.Page{}, fmt.Errorf("scan denylist entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return denylist.Page{}, fmt.Errorf("iterate denylist: %w", err)
	}

	page := denylist.Page{Entries: entries[:min(limit, len(entries))]}
	if len(entries) > limit {
		last := entries[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "denylist", Time: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Store) AddDenylistEntry(ctx context.Context, params denylist.AddParams) (denylist.Entry, error) {
	targetType := strings.TrimSpace(params.TargetType)
	targetID := strings.TrimSpace(params.TargetID)
	reason := strings.TrimSpace(params.Reason)
	if targetType != denylist.TargetConversation && targetType != denylist.TargetContact {
		return denylist.Entry{}, ErrInvalidArgument
	}
	if !isUUID(targetID) {
		return denylist.Entry{}, ErrInvalidArgument
	}
	if len(reason) > 500 {
		return denylist.Entry{}, ErrInvalidArgument
	}

	exists, err := s.denylistTargetExists(ctx, targetType, targetID)
	if err != nil {
		return denylist.Entry{}, err
	}
	if !exists {
		return denylist.Entry{}, ErrNotFound
	}

	var entry denylist.Entry
	err = s.pool.QueryRow(ctx, `
		INSERT INTO denylist_entries (target_type, target_id, reason)
		VALUES ($1, $2, NULLIF($3, ''))
		ON CONFLICT (target_type, target_id) DO UPDATE SET
			reason = COALESCE(EXCLUDED.reason, denylist_entries.reason)
		RETURNING id::text, target_type, target_id::text, COALESCE(reason, ''), created_at`,
		targetType, targetID, reason,
	).Scan(&entry.ID, &entry.TargetType, &entry.TargetID, &entry.Reason, &entry.CreatedAt)
	if err != nil {
		return denylist.Entry{}, fmt.Errorf("add denylist entry: %w", err)
	}
	entry.TargetLabel, _ = s.denylistTargetLabel(ctx, entry.TargetType, entry.TargetID)
	return entry, nil
}

func (s *Store) RemoveDenylistEntry(ctx context.Context, id string) error {
	if !isUUID(id) {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM denylist_entries WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("remove denylist entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeniedConversationProviderIDs returns provider conversation IDs that should not be ingested.
// Includes denied conversations plus contact identities (used as direct-chat JIDs).
func (s *Store) DeniedConversationProviderIDs(ctx context.Context, providerInstanceExternalID string) (map[string]struct{}, error) {
	denied := make(map[string]struct{})
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT provider_id FROM (
			SELECT c.provider_conversation_id AS provider_id
			FROM denylist_entries d
			JOIN conversations c ON c.id = d.target_id
			JOIN provider_instances pi ON pi.id = c.provider_instance_id
			WHERE d.target_type = 'conversation'
			  AND pi.provider_instance_id = $1
			UNION
			SELECT ci.identity AS provider_id
			FROM denylist_entries d
			JOIN contact_identities ci ON ci.contact_id = d.target_id
			JOIN provider_instances pi ON pi.id = ci.provider_instance_id
			WHERE d.target_type = 'contact'
			  AND pi.provider_instance_id = $1
			UNION
			SELECT ct.provider_contact_id AS provider_id
			FROM denylist_entries d
			JOIN contacts ct ON ct.id = d.target_id
			JOIN provider_instances pi ON pi.id = ct.provider_instance_id
			WHERE d.target_type = 'contact'
			  AND pi.provider_instance_id = $1
		) denied
		WHERE provider_id <> ''`, providerInstanceExternalID)
	if err != nil {
		return nil, fmt.Errorf("load denied conversation provider ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var providerID string
		if err := rows.Scan(&providerID); err != nil {
			return nil, fmt.Errorf("scan denied provider id: %w", err)
		}
		denied[providerID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate denied provider ids: %w", err)
	}
	return denied, nil
}

func (s *Store) denylistTargetExists(ctx context.Context, targetType, targetID string) (bool, error) {
	var query string
	switch targetType {
	case denylist.TargetConversation:
		query = `SELECT EXISTS(SELECT 1 FROM conversations WHERE id = $1)`
	case denylist.TargetContact:
		query = `SELECT EXISTS(SELECT 1 FROM contacts WHERE id = $1)`
	default:
		return false, ErrInvalidArgument
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, query, targetID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check denylist target: %w", err)
	}
	return exists, nil
}

func (s *Store) denylistTargetLabel(ctx context.Context, targetType, targetID string) (string, error) {
	var label string
	var err error
	switch targetType {
	case denylist.TargetConversation:
		err = s.pool.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''),
			                NULLIF(cc.phone, ''), NULLIF(c.provider_conversation_id, ''), '')
			FROM conversations c
			LEFT JOIN contacts cc ON cc.id = c.contact_id
			WHERE c.id = $1`, targetID).Scan(&label)
	case denylist.TargetContact:
		err = s.pool.QueryRow(ctx, `
			SELECT COALESCE(NULLIF(name, ''), NULLIF(push_name, ''), NULLIF(phone, ''),
			                NULLIF(provider_contact_id, ''), '')
			FROM contacts WHERE id = $1`, targetID).Scan(&label)
	default:
		return "", nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return label, nil
}
