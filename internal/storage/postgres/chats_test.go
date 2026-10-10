package postgres

import (
	"testing"

	"github.com/leofelipet/contexta/internal/chats"
)

func TestChatTitlePrefersPushNameOverPhone(t *testing.T) {
	t.Parallel()
	title := chatTitle(chats.Profile{
		Name: "5514999999999", PushName: "Talita", Phone: "5514999999999",
	})
	if title != "Talita" {
		t.Fatalf("title = %q, want Talita", title)
	}
}

func TestFirstHumanNameSkipsIdentifiers(t *testing.T) {
	t.Parallel()
	if got := firstHumanName("5511", "5511", "123@lid", "Ana"); got != "Ana" {
		t.Fatalf("got = %q", got)
	}
	if got := firstHumanName("5511999999999", "5511999999999"); got != "" {
		t.Fatalf("got = %q, want empty", got)
	}
}
