package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/leofelipet/contexta/internal/chats"
)

func (s *Store) SyncChats(ctx context.Context, providerInstanceID string, profiles []chats.Profile) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin chat sync: %w", err)
	}
	defer tx.Rollback(ctx)

	updated := 0
	for _, profile := range profiles {
		title := chatTitle(profile)
		if title == "" {
			continue
		}
		tag, err := tx.Exec(ctx, `
			UPDATE conversations c SET title = $4, updated_at = now()
			FROM provider_instances pi
			WHERE c.provider_instance_id = pi.id
			  AND pi.provider = 'uazapi'
			  AND pi.provider_instance_id = $1
			  AND c.provider_conversation_id IN ($2, $3)`,
			providerInstanceID, profile.JID, profile.LID, title)
		if err != nil {
			return 0, fmt.Errorf("sync conversation title: %w", err)
		}
		updated += int(tag.RowsAffected())

		if profile.Group {
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE contacts contact SET
				phone = CASE WHEN $4 = '' THEN contact.phone ELSE $4 END,
				name = CASE WHEN $5 = '' THEN contact.name ELSE $5 END,
				push_name = CASE WHEN $6 = '' THEN contact.push_name ELSE $6 END,
				updated_at = now()
			FROM conversations c, provider_instances pi
			WHERE c.contact_id = contact.id
			  AND c.provider_instance_id = pi.id
			  AND pi.provider = 'uazapi'
			  AND pi.provider_instance_id = $1
			  AND c.provider_conversation_id IN ($2, $3)`,
			providerInstanceID, profile.JID, profile.LID, profile.Phone,
			firstNonEmptyString(profile.ContactName, profile.Name), profile.PushName); err != nil {
			return 0, fmt.Errorf("sync conversation contact: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit chat sync: %w", err)
	}
	return updated, nil
}

func chatTitle(profile chats.Profile) string {
	values := []string{profile.ContactName, profile.Name, profile.PushName, profile.Phone}
	if profile.Group {
		values = []string{profile.Name, profile.PushName}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !isWhatsAppIdentifier(value) {
			return value
		}
	}
	return ""
}

func isWhatsAppIdentifier(value string) bool {
	for _, suffix := range []string{"@lid", "@s.whatsapp.net", "@g.us", "@newsletter"} {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
