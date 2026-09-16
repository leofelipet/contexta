package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/leofelipe/contexta/internal/contacts"
	"github.com/leofelipe/contexta/internal/conversations"
	"github.com/leofelipe/contexta/internal/ingestion"
	"github.com/leofelipe/contexta/internal/messages"
)

func TestIngestionAndQueries(t *testing.T) {
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

	suffix := fmt.Sprint(time.Now().UnixNano())
	instance := "test-instance-" + suffix
	chat := "5511999999999@s.whatsapp.net"
	contact := ingestion.Contact{
		ProviderID: chat, Phone: "5511999999999", Name: "Alice " + suffix,
		Identities: []ingestion.Identity{{Kind: "pn", Value: chat}},
	}
	baseTime := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	pendingResult, err := store.IngestMessages(ctx, ingestion.Batch{
		Provider: "uazapi", ProviderInstanceID: instance,
		Messages: []ingestion.Message{{
			UpdateOnly: true, ProviderMessageID: fmt.Sprintf("message-%s-1", suffix), Status: "Read",
		}},
	})
	if err != nil || pendingResult.Updated != 1 {
		t.Fatalf("pending update = %#v, error = %v", pendingResult, err)
	}

	batch := ingestion.Batch{Provider: "uazapi", ProviderInstanceID: instance}
	for i, text := range []string{"antes do contrato", "enviar contrato assinado", "depois do contrato"} {
		batch.Messages = append(batch.Messages, ingestion.Message{
			ProviderMessageID: fmt.Sprintf("message-%s-%d", suffix, i),
			Conversation:      ingestion.Conversation{ProviderID: chat, Type: "direct", Title: contact.Name, Contact: &contact},
			Sender:            &contact, Direction: "inbound", Type: "text", Text: text, Status: "Sent",
			OccurredAt: baseTime.Add(time.Duration(i) * time.Minute),
		})
	}
	batch.Messages[0].ReplyToProviderMessageID = batch.Messages[2].ProviderMessageID
	result, err := store.IngestMessages(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 3 {
		t.Fatalf("created = %d, want 3", result.Created)
	}

	result, err = store.IngestMessages(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 0 || result.Updated != 3 {
		t.Fatalf("duplicate result = %#v", result)
	}

	conversationPage, err := store.ListConversations(ctx, conversations.ListParams{Query: suffix, Limit: 10})
	if err != nil || len(conversationPage.Conversations) != 1 {
		t.Fatalf("conversations = %#v, error = %v", conversationPage, err)
	}
	conversationID := conversationPage.Conversations[0].ID
	allMessages, err := store.SearchMessages(ctx, messages.SearchParams{ConversationID: conversationID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range allMessages.Messages {
		switch message.ProviderMessageID {
		case batch.Messages[1].ProviderMessageID:
			if message.Status != "Read" {
				t.Fatalf("pending status = %q, want Read", message.Status)
			}
		case batch.Messages[0].ProviderMessageID:
			if message.ReplyToMessageID == "" {
				t.Fatal("reply was not backfilled")
			}
		}
	}

	messagePage, err := store.SearchMessages(ctx, messages.SearchParams{
		Query: "contrato", ConversationID: conversationID, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(messagePage.Messages) != 2 || messagePage.NextCursor == "" {
		t.Fatalf("message page = %#v", messagePage)
	}
	secondPage, err := store.SearchMessages(ctx, messages.SearchParams{
		Query: "contrato", ConversationID: conversationID, Limit: 2, Cursor: messagePage.NextCursor,
	})
	if err != nil || len(secondPage.Messages) != 1 {
		t.Fatalf("second page = %#v, error = %v", secondPage, err)
	}

	anchor := messagePage.Messages[1]
	around, err := store.GetMessagesAround(ctx, anchor.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(around.Previous) != 1 || len(around.Next) != 1 {
		t.Fatalf("around = %#v", around)
	}

	contactPage, err := store.ListContacts(ctx, contacts.ListParams{Query: suffix, Limit: 10})
	if err != nil || len(contactPage.Contacts) != 1 {
		t.Fatalf("contacts = %#v, error = %v", contactPage, err)
	}
}
