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

func TestServiceValidatesBeforeStore(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	service := NewService(store)
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
