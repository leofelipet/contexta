package transcription

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type RemoteError struct {
	Provider   string
	StatusCode int
	Delay      time.Duration
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("%s returned status %d", e.Provider, e.StatusCode)
}

func (e *RemoteError) RetryAfter() time.Duration {
	return e.Delay
}

func RetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if timestamp, err := http.ParseTime(value); err == nil && timestamp.After(now) {
		return timestamp.Sub(now)
	}
	return 0
}

type Job struct {
	MessageID         string
	ProviderMessageID string
	Attempts          int
}

type Audio struct {
	Data     []byte
	MIMEType string
	FileName string
}

type Result struct {
	Text     string
	Language string
	Model    string
}

type Store interface {
	ClaimTranscription(context.Context, time.Duration) (Job, bool, error)
	CompleteTranscription(context.Context, Job, Result) error
	RetryTranscription(context.Context, Job, string, time.Time, bool) error
}

type Downloader interface {
	DownloadAudio(context.Context, string, int64) (Audio, error)
}

type Transcriber interface {
	Transcribe(context.Context, Audio, string) (Result, error)
}
