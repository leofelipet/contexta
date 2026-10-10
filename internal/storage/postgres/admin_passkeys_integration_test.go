package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/leofelipet/contexta/internal/passkeys"
)

func TestAdminPasskeys(t *testing.T) {
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

	created, err := store.CreateAdminPasskey(ctx, passkeys.CreateParams{
		ID: "passkeyTestOne", PublicKey: "publicKeyOne", Name: "iPhone",
		Transports: []string{"internal"}, DeviceType: "multiDevice",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.DeleteAdminPasskey(ctx, created.ID)
	})
	if created.Name != "iPhone" || created.Counter != 0 {
		t.Fatalf("created = %#v", created)
	}

	if _, err := store.CreateAdminPasskey(ctx, passkeys.CreateParams{
		ID: created.ID, PublicKey: "publicKeyTwo",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate err = %v, want conflict", err)
	}

	updated, err := store.UpdateAdminPasskeyCounter(ctx, created.ID, 3)
	if err != nil || updated.Counter != 3 {
		t.Fatalf("updated = %#v, err = %v", updated, err)
	}

	listed, err := store.ListAdminPasskeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range listed {
		if item.ID == created.ID {
			found = true
			if item.Counter != 3 {
				t.Fatalf("listed counter = %d", item.Counter)
			}
		}
	}
	if !found {
		t.Fatal("created passkey missing from list")
	}

	if err := store.DeleteAdminPasskey(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAdminPasskey(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err = %v, want not found", err)
	}
}
