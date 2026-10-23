package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/leofelipet/contexta/internal/tasks"
	"github.com/leofelipet/contexta/internal/trello"
)

func TestSyncTrelloCardIsIdempotent(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if err := Migrate(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cardID := "trello-card-" + time.Now().UTC().Format("20060102150405.000000000")
	first := trello.CardSync{
		CardID: cardID, Title: "First title", Description: "First description", Status: tasks.StatusPending,
		EventAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	}
	created, applied, err := store.SyncTrelloCard(ctx, first)
	if err != nil || !applied || created.Title != first.Title {
		t.Fatalf("created = %+v, applied = %t, err = %v", created, applied, err)
	}

	duplicate := first
	duplicate.Title = "Ignored duplicate"
	unchanged, applied, err := store.SyncTrelloCard(ctx, duplicate)
	if err != nil || applied || unchanged.ID != created.ID || unchanged.Title != first.Title {
		t.Fatalf("unchanged = %+v, applied = %t, err = %v", unchanged, applied, err)
	}

	updated := first
	updated.Title = "Updated title"
	updated.Status = tasks.StatusInProgress
	updated.EventAt = updated.EventAt.Add(time.Minute)
	synced, applied, err := store.SyncTrelloCard(ctx, updated)
	if err != nil || !applied || synced.ID != created.ID || synced.Title != updated.Title || synced.Status != updated.Status {
		t.Fatalf("synced = %+v, applied = %t, err = %v", synced, applied, err)
	}
}
