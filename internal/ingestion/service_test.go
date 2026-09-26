package ingestion

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	batch Batch
}

func (f *fakeStore) IngestMessages(_ context.Context, batch Batch) (Result, error) {
	f.batch = batch
	return Result{Processed: len(batch.Messages)}, nil
}

type fakeDenylist struct {
	denied map[string]struct{}
}

func (f fakeDenylist) DeniedConversationProviderIDs(_ context.Context, _ string) (map[string]struct{}, error) {
	return f.denied, nil
}

func TestServiceValidatesBeforeStore(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	service := NewService(store, nil)
	_, err := service.Ingest(context.Background(), Batch{
		Provider: "uazapi", ProviderInstanceID: "instance",
		Messages: []Message{{Direction: "inbound", OccurredAt: time.Now()}},
	})
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("error = %v, want ErrInvalidMessage", err)
	}
	if len(store.batch.Messages) != 0 {
		t.Fatal("store called for invalid message")
	}
}

func TestServiceFiltersDeniedConversations(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	service := NewService(store, fakeDenylist{denied: map[string]struct{}{"denied@g.us": {}}})
	now := time.Now().UTC()
	_, err := service.Ingest(context.Background(), Batch{
		Provider: "uazapi", ProviderInstanceID: "instance",
		Messages: []Message{
			{
				ProviderMessageID: "keep-1", Direction: "inbound", OccurredAt: now,
				Conversation: Conversation{ProviderID: "ok@s.whatsapp.net", Type: "direct"},
			},
			{
				ProviderMessageID: "deny-1", Direction: "inbound", OccurredAt: now,
				Conversation: Conversation{ProviderID: "denied@g.us", Type: "group"},
			},
			{
				UpdateOnly: true, ProviderMessageID: "update-1", Status: "Read",
			},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(store.batch.Messages) != 2 {
		t.Fatalf("kept = %d, want 2", len(store.batch.Messages))
	}
	if store.batch.Messages[0].ProviderMessageID != "keep-1" || store.batch.Messages[1].ProviderMessageID != "update-1" {
		t.Fatalf("unexpected kept messages: %#v", store.batch.Messages)
	}
}
