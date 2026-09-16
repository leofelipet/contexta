package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/transcription"
)

func (s *Store) ClaimTranscription(ctx context.Context, lease time.Duration) (transcription.Job, bool, error) {
	var job transcription.Job
	err := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT j.message_id
			FROM transcription_jobs j
			WHERE (j.status IN ('pending', 'retry') AND j.next_attempt_at <= now())
			   OR (j.status = 'processing' AND j.lease_until < now())
			ORDER BY j.next_attempt_at, j.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE transcription_jobs j SET
			status = 'processing',
			attempts = attempts + 1,
			lease_until = now() + $1::interval,
			updated_at = now()
		FROM candidate c, messages m
		WHERE j.message_id = c.message_id
		  AND m.id = j.message_id
		RETURNING j.message_id::text, m.provider_message_id, j.attempts`, lease.String()).Scan(
		&job.MessageID, &job.ProviderMessageID, &job.Attempts,
	)
	if err == pgx.ErrNoRows {
		return transcription.Job{}, false, nil
	}
	if err != nil {
		return transcription.Job{}, false, fmt.Errorf("claim transcription: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE messages SET transcription_status = 'processing', updated_at = now()
		WHERE id = $1`, job.MessageID); err != nil {
		return transcription.Job{}, false, fmt.Errorf("mark transcription processing: %w", err)
	}
	return job, true, nil
}

func (s *Store) CompleteTranscription(ctx context.Context, job transcription.Job, result transcription.Result) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transcription completion: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE messages SET
			transcription_text = $2,
			transcription_status = 'completed',
			transcription_language = $3,
			transcription_model = $4,
			transcribed_at = now(),
			updated_at = now()
		WHERE id = $1`, job.MessageID, result.Text, result.Language, result.Model); err != nil {
		return fmt.Errorf("save transcription: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transcription_jobs SET
			status = 'completed', lease_until = NULL, last_error = '', updated_at = now()
		WHERE message_id = $1`, job.MessageID); err != nil {
		return fmt.Errorf("complete transcription job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transcription: %w", err)
	}
	return nil
}

func (s *Store) RetryTranscription(ctx context.Context, job transcription.Job, message string, retryAt time.Time, failed bool) error {
	status := "retry"
	if failed {
		status = "failed"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transcription retry: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE transcription_jobs SET
			status = $2, next_attempt_at = $3, lease_until = NULL,
			last_error = $4, updated_at = now()
		WHERE message_id = $1`, job.MessageID, status, retryAt, message); err != nil {
		return fmt.Errorf("retry transcription job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messages SET transcription_status = $2, updated_at = now()
		WHERE id = $1`, job.MessageID, status); err != nil {
		return fmt.Errorf("mark transcription retry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transcription retry: %w", err)
	}
	return nil
}
