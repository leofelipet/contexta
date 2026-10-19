package schedules

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParseSendAt(t *testing.T) {
	t.Parallel()
	saoPaulo13h := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
	cases := []struct {
		value, timezone string
		want            time.Time
	}{
		{"2026-10-19T13:00:00-03:00", "", saoPaulo13h},
		{"2026-10-19T16:00:00Z", "Europe/Lisbon", saoPaulo13h},
		{"2026-10-19T13:00", "", saoPaulo13h},
		{"2026-10-19 13:00:00", "America/Sao_Paulo", saoPaulo13h},
		{"2026-10-19T13:00", "UTC", time.Date(2026, 10, 19, 13, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got, err := ParseSendAt(c.value, c.timezone)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("ParseSendAt(%q, %q) = %v, %v; want %v", c.value, c.timezone, got, err, c.want)
		}
	}
	for _, c := range [][2]string{{"", ""}, {"amanhã 13h", ""}, {"2026-10-19", ""}, {"2026-10-19T13:00", "Mars/Base"}} {
		if _, err := ParseSendAt(c[0], c[1]); !errors.Is(err, ErrInvalidNotification) {
			t.Errorf("ParseSendAt(%q, %q) err = %v, want ErrInvalidNotification", c[0], c[1], err)
		}
	}
}

type fakeRunner struct {
	schedules, reminders chan Notifier
}

func (f fakeRunner) RunDueTaskSchedules(_ context.Context, _ time.Time, notify Notifier) (RunResult, error) {
	f.schedules <- notify
	return RunResult{}, nil
}

func (f fakeRunner) RunDueReminders(_ context.Context, _ time.Time, notify Notifier) (ReminderRunResult, error) {
	f.reminders <- notify
	return ReminderRunResult{}, nil
}

func TestWorkerRunsSchedulesAndRemindersAtStartup(t *testing.T) {
	t.Parallel()
	runner := fakeRunner{schedules: make(chan Notifier, 1), reminders: make(chan Notifier, 1)}
	notify := Notifier(func(context.Context, Notification) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewWorker(runner, notify, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx)

	for name, ch := range map[string]chan Notifier{"schedules": runner.schedules, "reminders": runner.reminders} {
		select {
		case got := <-ch:
			if got == nil {
				t.Fatalf("%s ran without notifier", name)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s did not run at startup", name)
		}
	}
}

func TestWorkerWithoutNotifierSkipsReminders(t *testing.T) {
	t.Parallel()
	runner := fakeRunner{schedules: make(chan Notifier, 1), reminders: make(chan Notifier, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go NewWorker(runner, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx)

	select {
	case <-runner.schedules:
	case <-time.After(2 * time.Second):
		t.Fatal("schedules did not run at startup")
	}
	select {
	case <-runner.reminders:
		t.Fatal("reminders ran without a notifier")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestScheduleNotification(t *testing.T) {
	t.Parallel()
	got := ScheduleNotification("Banheiro", "  ", 1, "siren")
	if got != (Notification{Title: "Banheiro", Message: "Banheiro", Priority: 1, Sound: "siren"}) {
		t.Fatalf("notification = %+v", got)
	}
	long := strings.Repeat("é", 2000)
	got = ScheduleNotification(long, long, 0, "")
	if len([]rune(got.Title)) != 250 || len([]rune(got.Message)) != 1024 {
		t.Fatalf("title runes = %d, message runes = %d", len([]rune(got.Title)), len([]rune(got.Message)))
	}
}
