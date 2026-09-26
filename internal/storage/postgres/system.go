package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/leofelipet/contexta/internal/admin"
)

func (s *Store) SystemOverview(ctx context.Context) (admin.SystemOverview, error) {
	var database admin.SystemDatabase
	var lastMessage, lastWebhook pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		SELECT pg_database_size(current_database())::bigint,
		       (SELECT count(*)::bigint FROM messages WHERE occurred_at >= now() - interval '24 hours'),
		       (SELECT max(occurred_at) FROM messages),
		       (SELECT max(occurred_at) FROM activity_events WHERE category = 'webhook'),
		       (SELECT count(*)::bigint FROM denylist_entries)
	`).Scan(
		&database.SizeBytes,
		&database.MessagesLast24H,
		&lastMessage,
		&lastWebhook,
		&database.DenylistEntries,
	)
	if err != nil {
		return admin.SystemOverview{}, fmt.Errorf("load system database metrics: %w", err)
	}
	if lastMessage.Valid {
		database.LastMessageAt = &lastMessage.Time
	}
	if lastWebhook.Valid {
		database.LastWebhookAt = &lastWebhook.Time
	}

	var migrationTable pgtype.Text
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.goose_db_version')::text`).Scan(&migrationTable); err != nil {
		return admin.SystemOverview{}, fmt.Errorf("check goose table: %w", err)
	}
	if migrationTable.Valid {
		var version pgtype.Int8
		if err := s.pool.QueryRow(ctx, `SELECT max(version_id) FILTER (WHERE is_applied) FROM goose_db_version`).Scan(&version); err != nil {
			return admin.SystemOverview{}, fmt.Errorf("load migration version: %w", err)
		}
		if version.Valid {
			database.MigrationVersion = &version.Int64
		}
	}

	var queues admin.SystemQueues
	err = s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'pending')::bigint,
			count(*) FILTER (WHERE status = 'processing')::bigint,
			count(*) FILTER (WHERE status = 'retry')::bigint,
			count(*) FILTER (WHERE status = 'failed')::bigint
		FROM transcription_jobs
	`).Scan(
		&queues.TranscriptionPending,
		&queues.TranscriptionProcessing,
		&queues.TranscriptionRetry,
		&queues.TranscriptionFailed,
	)
	if err != nil {
		return admin.SystemOverview{}, fmt.Errorf("load transcription queues: %w", err)
	}

	stats := s.pool.Stat()
	return admin.SystemOverview{
		Database: database,
		Pool: admin.SystemPool{
			MaxConnections:      stats.MaxConns(),
			TotalConnections:    stats.TotalConns(),
			IdleConnections:     stats.IdleConns(),
			AcquiredConnections: stats.AcquiredConns(),
		},
		Queues: queues,
	}, nil
}
