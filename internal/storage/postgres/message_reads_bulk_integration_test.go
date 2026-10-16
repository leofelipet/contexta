package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/leofelipet/contexta/internal/ingestion"
	"github.com/leofelipet/contexta/internal/messages"
)

func TestBulkAcknowledgeAndCountUnread(t *testing.T) {
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

	now := time.Now().UnixNano()
	suffix := fmt.Sprint(now)
	consumer := "bulk-" + suffix
	chat := "5511888888888@s.whatsapp.net"
	contact := ingestion.Contact{ProviderID: chat, Phone: "5511888888888", Name: "Bulk " + suffix}
	// A far-future year unique to this run keeps the global unread count isolated from other data.
	year := 2100 + int(now/1000%800)
	// 01:00 UTC on Feb 1st is still January in America/Sao_Paulo.
	times := []time.Time{
		time.Date(year, 1, 10, 12, 0, 0, 0, time.UTC),
		time.Date(year, 2, 1, 1, 0, 0, 0, time.UTC),
		time.Date(year, 2, 15, 12, 0, 0, 0, time.UTC),
		time.Date(year, 3, 5, 12, 0, 0, 0, time.UTC),
	}
	batch := ingestion.Batch{Provider: "uazapi", ProviderInstanceID: "bulk-instance-" + suffix}
	for i, occurredAt := range times {
		direction := "inbound"
		if i == 2 {
			direction = "outbound"
		}
		batch.Messages = append(batch.Messages, ingestion.Message{
			ProviderMessageID: fmt.Sprintf("bulk-%s-%d", suffix, i),
			Conversation:      ingestion.Conversation{ProviderID: chat, Type: "direct", Title: contact.Name, Contact: &contact},
			Sender:            &contact, Direction: direction, Type: "text", Text: fmt.Sprint("bulk ", i),
			OccurredAt: occurredAt,
		})
	}
	if _, err := store.IngestMessages(ctx, batch); err != nil {
		t.Fatal(err)
	}
	yearStart := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := yearStart.AddDate(1, 0, 0)

	count, err := store.CountUnreadMessages(ctx, messages.UnreadCountParams{
		ConsumerID: consumer, After: &yearStart, Before: &yearEnd, Timezone: "America/Sao_Paulo",
	})
	want := []messages.UnreadMonthCount{{Month: fmt.Sprintf("%d-01", year), Count: 2}, {Month: fmt.Sprintf("%d-02", year), Count: 1}, {Month: fmt.Sprintf("%d-03", year), Count: 1}}
	if err != nil || count.Total != 4 || fmt.Sprint(count.Months) != fmt.Sprint(want) {
		t.Fatalf("count = %#v, err = %v", count, err)
	}

	// Draining without a cursor: acknowledged messages leave the first page.
	first, err := store.ListUnreadMessages(ctx, messages.UnreadParams{ConsumerID: consumer + "-drain", Order: "oldest", Limit: 1, Type: "text"})
	if err != nil || len(first.Messages) != 1 {
		t.Fatalf("first drain page = %#v, err = %v", first, err)
	}
	if _, err := store.AcknowledgeMessages(ctx, consumer+"-drain", []string{first.Messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	second, err := store.ListUnreadMessages(ctx, messages.UnreadParams{ConsumerID: consumer + "-drain", Order: "oldest", Limit: 1, Type: "text"})
	if err != nil || len(second.Messages) != 1 || second.Messages[0].ID == first.Messages[0].ID {
		t.Fatalf("second drain page repeated = %#v, err = %v", second, err)
	}

	before := time.Date(year, 3, 1, 0, 0, 0, 0, time.UTC)
	dryRun, err := store.AcknowledgeMessagesBulk(ctx, messages.BulkAcknowledgeParams{
		ConsumerID: consumer, Before: before, After: &yearStart, DryRun: true,
	})
	if err != nil || dryRun.Matched != 3 || dryRun.Acknowledged != 0 || dryRun.OldestMessageAt == nil ||
		!dryRun.OldestMessageAt.Equal(times[0]) || !dryRun.NewestMessageAt.Equal(times[2]) {
		t.Fatalf("dry run = %#v, err = %v", dryRun, err)
	}

	inbound, err := store.AcknowledgeMessagesBulk(ctx, messages.BulkAcknowledgeParams{
		ConsumerID: consumer, Before: before, After: &yearStart, Direction: "incoming",
	})
	if err != nil || inbound.Matched != 2 || inbound.Acknowledged != 2 || inbound.AlreadyAcknowledged != 0 {
		t.Fatalf("inbound = %#v, err = %v", inbound, err)
	}
	all, err := store.AcknowledgeMessagesBulk(ctx, messages.BulkAcknowledgeParams{
		ConsumerID: consumer, Before: before, After: &yearStart, Direction: "all",
	})
	if err != nil || all.Matched != 1 || all.Acknowledged != 1 || all.AlreadyAcknowledged != 2 {
		t.Fatalf("all = %#v, err = %v", all, err)
	}

	count, err = store.CountUnreadMessages(ctx, messages.UnreadCountParams{
		ConsumerID: consumer, After: &yearStart, Before: &yearEnd, Timezone: "UTC",
	})
	if err != nil || count.Total != 1 || count.Months[0].Month != fmt.Sprintf("%d-03", year) {
		t.Fatalf("remaining count = %#v, err = %v", count, err)
	}

	if _, err := store.AcknowledgeMessagesBulk(ctx, messages.BulkAcknowledgeParams{ConsumerID: consumer, Before: before, Direction: "sideways"}); err != ErrInvalidArgument {
		t.Fatalf("invalid direction err = %v", err)
	}
	if _, err := store.AcknowledgeMessagesBulk(ctx, messages.BulkAcknowledgeParams{ConsumerID: consumer, Before: yearStart, After: &before}); err != ErrInvalidArgument {
		t.Fatalf("inverted range err = %v", err)
	}
}

func TestBulkAcknowledgeRequiresConfirmation(t *testing.T) {
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
	chat := "5511777777777@s.whatsapp.net"
	contact := ingestion.Contact{ProviderID: chat, Phone: "5511777777777", Name: "Confirm " + suffix}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	batch := ingestion.Batch{Provider: "uazapi", ProviderInstanceID: "confirm-instance-" + suffix}
	for i := range BulkAcknowledgeConfirmLimit + 1 {
		batch.Messages = append(batch.Messages, ingestion.Message{
			ProviderMessageID: fmt.Sprintf("confirm-%s-%d", suffix, i),
			Conversation:      ingestion.Conversation{ProviderID: chat, Type: "direct", Title: contact.Name, Contact: &contact},
			Sender:            &contact, Direction: "inbound", Type: "text", Text: "x",
			OccurredAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	if _, err := store.IngestMessages(ctx, batch); err != nil {
		t.Fatal(err)
	}
	params := messages.BulkAcknowledgeParams{ConsumerID: "confirm-" + suffix, Before: base.Add(time.Hour * 3), After: &base}
	blocked, err := store.AcknowledgeMessagesBulk(ctx, params)
	if err != nil || !blocked.ConfirmationRequired || blocked.Acknowledged != 0 || blocked.Matched < BulkAcknowledgeConfirmLimit+1 {
		t.Fatalf("blocked = %#v, err = %v", blocked, err)
	}
	params.Confirm = true
	confirmed, err := store.AcknowledgeMessagesBulk(ctx, params)
	if err != nil || confirmed.ConfirmationRequired || confirmed.Acknowledged != blocked.Matched {
		t.Fatalf("confirmed = %#v, err = %v", confirmed, err)
	}
}
