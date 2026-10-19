package postgres

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leofelipet/contexta/internal/schedules"
)

func TestReminders(t *testing.T) {
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

	makeDue := func(id string) {
		t.Helper()
		if _, err := store.pool.Exec(ctx, `UPDATE reminders SET send_at = now() - interval '1 minute' WHERE id = $1::bigint`, id); err != nil {
			t.Fatal(err)
		}
	}
	create := func(params schedules.ReminderCreateParams) schedules.Reminder {
		t.Helper()
		reminder, err := store.CreateReminder(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		return reminder
	}
	ok := func(context.Context, schedules.Notification) error { return nil }

	t.Run("create validates fields", func(t *testing.T) {
		future := time.Now().Add(time.Hour)
		bad := 3
		for name, params := range map[string]schedules.ReminderCreateParams{
			"missing message": {SendAt: future},
			"missing send_at": {Message: "x"},
			"past send_at":    {Message: "x", SendAt: time.Now().Add(-time.Hour)},
			"bad priority":    {Message: "x", SendAt: future, Priority: &bad},
			"bad sound":       {Message: "x", SendAt: future, Sound: "a b"},
		} {
			if _, err := store.CreateReminder(ctx, params); !errors.Is(err, ErrInvalidArgument) || !errors.Is(err, schedules.ErrInvalidNotification) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
		priority := 2
		reminder := create(schedules.ReminderCreateParams{Title: " Lembrete ", Message: " Ir ao banheiro ", Priority: &priority, Sound: "siren", SendAt: future})
		if reminder.Title != "Lembrete" || reminder.Message != "Ir ao banheiro" || reminder.Priority != 2 || reminder.Status != schedules.ReminderPending || !reminder.SendAt.Equal(future.UTC().Truncate(time.Microsecond)) {
			t.Fatalf("reminder = %#v", reminder)
		}
	})

	t.Run("due reminder is sent once", func(t *testing.T) {
		reminder := create(schedules.ReminderCreateParams{Message: "Banheiro", Sound: "siren", SendAt: time.Now().Add(time.Hour)})
		if result, err := store.RunDueReminders(ctx, time.Now().UTC(), ok); err != nil || slices.Contains(result.Sent, reminder.ID) {
			t.Fatalf("not-yet-due result = %#v, err = %v", result, err)
		}
		makeDue(reminder.ID)
		var sent []schedules.Notification
		var mu sync.Mutex
		notify := func(_ context.Context, notification schedules.Notification) error {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, notification)
			return nil
		}
		result, err := store.RunDueReminders(ctx, time.Now().UTC(), notify)
		if err != nil || !slices.Contains(result.Sent, reminder.ID) {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
		if !slices.Contains(sent, schedules.Notification{Message: "Banheiro", Sound: "siren"}) {
			t.Fatalf("sent = %#v", sent)
		}
		got, err := store.GetReminder(ctx, reminder.ID)
		if err != nil || got.Status != schedules.ReminderSent || got.SentAt == nil || got.Attempts != 1 {
			t.Fatalf("reminder = %#v, err = %v", got, err)
		}
		if result, _ := store.RunDueReminders(ctx, time.Now().UTC(), ok); slices.Contains(result.Sent, reminder.ID) {
			t.Fatalf("resent: %#v", result)
		}
		message := "changed"
		if _, err := store.UpdateReminder(ctx, reminder.ID, schedules.ReminderUpdateParams{Message: &message}); !errors.Is(err, ErrConflict) {
			t.Fatalf("update sent err = %v", err)
		}
	})

	t.Run("failures retry then mark failed", func(t *testing.T) {
		reminder := create(schedules.ReminderCreateParams{Message: "Falha", SendAt: time.Now().Add(time.Hour)})
		makeDue(reminder.ID)
		failing := func(context.Context, schedules.Notification) error { return errors.New("pushover is not configured") }
		tick := time.Now().UTC()
		for attempt := 1; attempt <= schedules.MaxReminderAttempts; attempt++ {
			tick = tick.Add(schedules.ReminderTickInterval)
			result, err := store.RunDueReminders(ctx, tick, failing)
			if err != nil {
				t.Fatal(err)
			}
			wantFailed := attempt == schedules.MaxReminderAttempts
			if slices.Contains(result.Failed, reminder.ID) != wantFailed || slices.Contains(result.Retrying, reminder.ID) == wantFailed {
				t.Fatalf("attempt %d result = %#v", attempt, result)
			}
		}
		got, err := store.GetReminder(ctx, reminder.ID)
		if err != nil || got.Status != schedules.ReminderFailed || got.Attempts != schedules.MaxReminderAttempts || got.LastError != "pushover is not configured" {
			t.Fatalf("reminder = %#v, err = %v", got, err)
		}
	})

	t.Run("failed attempt is not retried in the same tick", func(t *testing.T) {
		reminder := create(schedules.ReminderCreateParams{Message: "Uma vez por tick", SendAt: time.Now().Add(time.Hour)})
		makeDue(reminder.ID)
		var calls atomic.Int32
		failing := func(_ context.Context, notification schedules.Notification) error {
			if notification.Message == "Uma vez por tick" {
				calls.Add(1)
			}
			return errors.New("unavailable")
		}
		tick := time.Now().UTC().Add(10 * time.Minute)
		if _, err := store.RunDueReminders(ctx, tick, failing); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatalf("calls = %d", calls.Load())
		}
		if err := store.DeleteReminder(ctx, reminder.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("update, list, and delete pending reminder", func(t *testing.T) {
		reminder := create(schedules.ReminderCreateParams{Message: "Editar", SendAt: time.Now().Add(time.Hour)})
		later := time.Now().Add(2 * time.Hour)
		priority, sound := -1, "bike"
		updated, err := store.UpdateReminder(ctx, reminder.ID, schedules.ReminderUpdateParams{SendAt: &later, Priority: &priority, Sound: &sound})
		if err != nil || updated.Priority != -1 || updated.Sound != "bike" || !updated.SendAt.Equal(later.UTC().Truncate(time.Microsecond)) {
			t.Fatalf("updated = %#v, err = %v", updated, err)
		}
		past := time.Now().Add(-time.Hour)
		if _, err := store.UpdateReminder(ctx, reminder.ID, schedules.ReminderUpdateParams{SendAt: &past}); !errors.Is(err, schedules.ErrInvalidNotification) {
			t.Fatalf("past update err = %v", err)
		}
		page, err := store.ListReminders(ctx, schedules.ReminderListParams{Status: schedules.ReminderPending, Limit: 100})
		if err != nil || !slices.ContainsFunc(page.Reminders, func(item schedules.Reminder) bool { return item.ID == reminder.ID }) {
			t.Fatalf("pending list = %d, err = %v", len(page.Reminders), err)
		}
		if _, err := store.ListReminders(ctx, schedules.ReminderListParams{Status: "unknown"}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("bad status err = %v", err)
		}
		if err := store.DeleteReminder(ctx, reminder.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetReminder(ctx, reminder.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get deleted = %v", err)
		}
	})

	t.Run("concurrent ticks send once", func(t *testing.T) {
		reminder := create(schedules.ReminderCreateParams{Message: "Concorrente", SendAt: time.Now().Add(time.Hour)})
		makeDue(reminder.ID)
		var calls atomic.Int32
		notify := func(_ context.Context, notification schedules.Notification) error {
			if notification.Message == "Concorrente" {
				calls.Add(1)
			}
			return nil
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if _, err := store.RunDueReminders(ctx, time.Now().UTC(), notify); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatalf("calls = %d", calls.Load())
		}
	})
}
