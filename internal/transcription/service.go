package transcription

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	maxAudioBytes = 20 << 20
	maxAttempts   = 5
	leaseDuration = 5 * time.Minute
)

type Service struct {
	store       Store
	downloader  Downloader
	transcriber Transcriber
	language    string
	logger      *slog.Logger
}

func NewService(store Store, downloader Downloader, transcriber Transcriber, language string, logger *slog.Logger) *Service {
	return &Service{store: store, downloader: downloader, transcriber: transcriber, language: language, logger: logger}
}

func (s *Service) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		worked, err := s.processOne(ctx)
		if err != nil {
			s.logger.Error("transcription worker failed", "error", err)
		}
		delay := 2 * time.Second
		if worked {
			delay = 100 * time.Millisecond
		}
		timer.Reset(delay)
	}
}

func (s *Service) processOne(ctx context.Context) (bool, error) {
	job, ok, err := s.store.ClaimTranscription(ctx, leaseDuration)
	if err != nil || !ok {
		return false, err
	}

	audio, err := s.downloader.DownloadAudio(ctx, job.ProviderMessageID, maxAudioBytes)
	if err == nil {
		var result Result
		result, err = s.transcriber.Transcribe(ctx, audio, s.language)
		if err == nil {
			if err := s.store.CompleteTranscription(ctx, job, result); err != nil {
				return true, err
			}
			s.logger.Info("audio transcribed", "message_id", job.MessageID, "model", result.Model, "language", result.Language)
			return true, nil
		}
	}

	failed := job.Attempts >= maxAttempts
	delay := retryDelay(job.Attempts)
	var remoteError interface{ RetryAfter() time.Duration }
	if errors.As(err, &remoteError) && remoteError.RetryAfter() > delay {
		delay = remoteError.RetryAfter()
	}
	retryAt := time.Now().Add(delay)
	if err := s.store.RetryTranscription(ctx, job, safeError(err), retryAt, failed); err != nil {
		return true, err
	}
	s.logger.Warn("audio transcription deferred", "message_id", job.MessageID, "attempt", job.Attempts, "failed", failed)
	return true, nil
}

func retryDelay(attempt int) time.Duration {
	delays := [...]time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	if attempt < 1 {
		return delays[0]
	}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}

func safeError(err error) string {
	if err == nil {
		return "unknown transcription error"
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}
