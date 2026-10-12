package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/pushover"
)

func (s *Store) GetPushoverSettings(ctx context.Context) (pushover.Settings, error) {
	row := s.pool.QueryRow(ctx, `SELECT enabled, created_at, updated_at FROM pushover_settings WHERE singleton = TRUE`)
	return scanPushoverSettings(row)
}

func (s *Store) GetPushoverCredentials(ctx context.Context) (pushover.Settings, []byte, []byte, error) {
	var settings pushover.Settings
	var appToken, userKey []byte
	err := s.pool.QueryRow(ctx, `
		SELECT enabled, created_at, updated_at, app_token_ciphertext, user_key_ciphertext
		FROM pushover_settings WHERE singleton = TRUE`,
	).Scan(&settings.Enabled, &settings.CreatedAt, &settings.UpdatedAt, &appToken, &userKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return pushover.Settings{}, nil, nil, pushover.ErrNotConfigured
	}
	if err != nil {
		return pushover.Settings{}, nil, nil, fmt.Errorf("get pushover credentials: %w", err)
	}
	settings.Configured = true
	return settings, appToken, userKey, nil
}

func (s *Store) UpsertPushoverSettings(ctx context.Context, enabled bool, appToken, userKey []byte) (pushover.Settings, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO pushover_settings (singleton, app_token_ciphertext, user_key_ciphertext, enabled)
		VALUES (TRUE, $1, $2, $3)
		ON CONFLICT (singleton) DO UPDATE SET
			app_token_ciphertext = EXCLUDED.app_token_ciphertext,
			user_key_ciphertext = EXCLUDED.user_key_ciphertext,
			enabled = EXCLUDED.enabled,
			updated_at = now()
		RETURNING enabled, created_at, updated_at`, appToken, userKey, enabled)
	settings, err := scanPushoverSettings(row)
	if err != nil {
		return pushover.Settings{}, fmt.Errorf("upsert pushover settings: %w", err)
	}
	return settings, nil
}

func scanPushoverSettings(row scannable) (pushover.Settings, error) {
	var settings pushover.Settings
	err := row.Scan(&settings.Enabled, &settings.CreatedAt, &settings.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return pushover.Settings{}, pushover.ErrNotConfigured
	}
	if err != nil {
		return pushover.Settings{}, err
	}
	settings.Configured = true
	return settings, nil
}
