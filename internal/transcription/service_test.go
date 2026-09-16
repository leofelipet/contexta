package transcription

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

type fakeStore struct {
	job       Job
	completed *Result
	retried   bool
	failed    bool
}

func (s *fakeStore) ClaimTranscription(context.Context, time.Duration) (Job, bool, error) {
	return s.job, true, nil
}

func (s *fakeStore) CompleteTranscription(_ context.Context, _ Job, result Result) error {
	s.completed = &result
	return nil
}

func (s *fakeStore) RetryTranscription(_ context.Context, _ Job, _ string, _ time.Time, failed bool) error {
	s.retried = true
	s.failed = failed
	return nil
}

type fakeDownloader struct{ err error }

func (d fakeDownloader) DownloadAudio(context.Context, string, int64) (Audio, error) {
	return Audio{Data: []byte("audio"), MIMEType: "audio/ogg", FileName: "audio.ogg"}, d.err
}

type fakeTranscriber struct{ err error }

func (t fakeTranscriber) Transcribe(context.Context, Audio, string) (Result, error) {
	return Result{Text: "texto", Language: "pt", Model: "whisper-large-v3-turbo"}, t.err
}

func TestProcessOneCompletesTranscription(t *testing.T) {
	t.Parallel()
	store := &fakeStore{job: Job{MessageID: "message-1", ProviderMessageID: "provider-1", Attempts: 1}}
	service := NewService(store, fakeDownloader{}, fakeTranscriber{}, "pt", slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := service.processOne(context.Background())
	if err != nil || !worked || store.completed == nil || store.completed.Text != "texto" {
		t.Fatalf("worked=%v completed=%#v err=%v", worked, store.completed, err)
	}
}

func TestProcessOneRetriesAndEventuallyFails(t *testing.T) {
	t.Parallel()
	store := &fakeStore{job: Job{MessageID: "message-1", ProviderMessageID: "provider-1", Attempts: maxAttempts}}
	service := NewService(store, fakeDownloader{err: errors.New("download failed")}, fakeTranscriber{}, "pt", slog.New(slog.NewTextHandler(io.Discard, nil)))
	worked, err := service.processOne(context.Background())
	if err != nil || !worked || !store.retried || !store.failed {
		t.Fatalf("worked=%v retried=%v failed=%v err=%v", worked, store.retried, store.failed, err)
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	if delay := RetryAfter("45", now); delay != 45*time.Second {
		t.Fatalf("delay = %v", delay)
	}
	if delay := RetryAfter(now.Add(time.Minute).Format(http.TimeFormat), now); delay != time.Minute {
		t.Fatalf("date delay = %v", delay)
	}
}
