package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/leofelipet/contexta/internal/mcpkeys"
)

func TestMCPKeys(t *testing.T) {
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

	token, prefix, hash, err := mcpkeys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateMCPKey(ctx, mcpkeys.Params{Name: " Claude ", Access: mcpkeys.AccessCustom, Tools: []string{"get_task", "get_task"}}, prefix, hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteMCPKey(ctx, created.ID) })
	if created.Name != "Claude" || len(created.Tools) != 1 || created.LastUsedAt != nil {
		t.Fatalf("created = %#v", created)
	}

	used, err := store.AuthenticateMCPKey(ctx, mcpkeys.Hash(token))
	if err != nil || used.ID != created.ID || used.LastUsedAt == nil {
		t.Fatalf("authenticate = %#v, %v", used, err)
	}
	if _, err := store.AuthenticateMCPKey(ctx, mcpkeys.Hash("wrong")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong token err = %v", err)
	}

	updated, err := store.UpdateMCPKey(ctx, created.ID, mcpkeys.Params{Name: "Claude", Access: mcpkeys.AccessRead, Tools: []string{"get_task"}})
	if err != nil || updated.Access != mcpkeys.AccessRead || len(updated.Tools) != 0 {
		t.Fatalf("updated = %#v, %v", updated, err)
	}
	if _, err := store.UpdateMCPKey(ctx, created.ID, mcpkeys.Params{Name: "x", Access: mcpkeys.AccessCustom}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("custom without tools err = %v", err)
	}

	past := time.Now().Add(-time.Hour)
	if _, err := store.CreateMCPKey(ctx, mcpkeys.Params{Name: "old", Access: mcpkeys.AccessRead, ExpiresAt: &past}, "p", "h-past"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("past expiry err = %v", err)
	}
	_, _, expiringHash, _ := mcpkeys.Generate()
	soon := time.Now().Add(time.Hour)
	expiring, err := store.CreateMCPKey(ctx, mcpkeys.Params{Name: "soon", Access: mcpkeys.AccessRead, ExpiresAt: &soon}, "p", expiringHash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteMCPKey(ctx, expiring.ID) })
	if _, err := store.pool.Exec(ctx, `UPDATE mcp_keys SET expires_at = now() - interval '1 second' WHERE id = $1`, expiring.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateMCPKey(ctx, expiringHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired key err = %v", err)
	}
}
