package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidMessage = errors.New("invalid normalized message")

type Contact struct {
	ProviderID        string
	Identities        []Identity
	Phone             string
	Name              string
	PushName          string
	ProfilePictureURL string
	Metadata          json.RawMessage
}

type Identity struct {
	Kind  string
	Value string
}

type Conversation struct {
	ProviderID string
	Type       string
	Title      string
	Contact    *Contact
	Metadata   json.RawMessage
}

type Message struct {
	UpdateOnly               bool
	ProviderMessageID        string
	ProviderRecordID         string
	Conversation             Conversation
	Sender                   *Contact
	Direction                string
	Type                     string
	Text                     string
	Status                   string
	OccurredAt               time.Time
	ReplyToProviderMessageID string
	Metadata                 json.RawMessage
}

type Batch struct {
	Provider           string
	ProviderInstanceID string
	Messages           []Message
}

type Result struct {
	Processed int `json:"processed"`
	Created   int `json:"created"`
	Updated   int `json:"updated"`
}

type Store interface {
	IngestMessages(context.Context, Batch) (Result, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) Ingest(ctx context.Context, batch Batch) (Result, error) {
	if strings.TrimSpace(batch.Provider) == "" || strings.TrimSpace(batch.ProviderInstanceID) == "" {
		return Result{}, fmt.Errorf("%w: provider and instance are required", ErrInvalidMessage)
	}
	if len(batch.Messages) == 0 {
		return Result{}, nil
	}
	for i := range batch.Messages {
		if err := validateMessage(batch.Messages[i]); err != nil {
			return Result{}, fmt.Errorf("message %d: %w", i, err)
		}
	}
	return s.store.IngestMessages(ctx, batch)
}

func validateMessage(message Message) error {
	if strings.TrimSpace(message.ProviderMessageID) == "" {
		return fmt.Errorf("%w: provider message id is required", ErrInvalidMessage)
	}
	if message.UpdateOnly {
		return nil
	}
	if strings.TrimSpace(message.Conversation.ProviderID) == "" {
		return fmt.Errorf("%w: conversation id is required", ErrInvalidMessage)
	}
	if message.Direction != "inbound" && message.Direction != "outbound" {
		return fmt.Errorf("%w: direction must be inbound or outbound", ErrInvalidMessage)
	}
	if message.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidMessage)
	}
	return nil
}
