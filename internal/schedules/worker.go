package schedules

import (
	"context"
	"log/slog"
	"time"
)

type Runner interface {
	RunDueTaskSchedules(ctx context.Context, now time.Time, notify Notifier) (RunResult, error)
	RunDueReminders(ctx context.Context, now time.Time, notify Notifier) (ReminderRunResult, error)
}

type Worker struct {
	runner Runner
	notify Notifier
	logger *slog.Logger
}

// NewWorker builds the scheduler worker. A nil notify skips schedule
// notifications and leaves reminders pending.
func NewWorker(runner Runner, notify Notifier, logger *slog.Logger) *Worker {
	return &Worker{runner: runner, notify: notify, logger: logger}
}

// Run fires once at startup (to catch up on runs missed while down) and then
// on every wall-clock minute. Reminders are delivered on every tick; task
// schedules run once per TickInterval window (:00, :05, :10…).
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastScheduleSlot time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		now := time.Now()
		if slot := now.Truncate(TickInterval); !slot.Equal(lastScheduleSlot) {
			lastScheduleSlot = slot
			w.tickSchedules(ctx)
		}
		w.tickReminders(ctx)
		timer.Reset(untilNext(time.Now(), ReminderTickInterval))
	}
}

func (w *Worker) tickSchedules(ctx context.Context) {
	result, err := w.runner.RunDueTaskSchedules(ctx, time.Now().UTC(), w.notify)
	if err != nil {
		w.logger.Error("task scheduler tick failed", "error", err)
	}
	if len(result.Created) > 0 || len(result.Skipped) > 0 || len(result.Failed) > 0 || len(result.NotifyFailed) > 0 {
		w.logger.Info("task scheduler tick",
			"created_tasks", result.Created, "skipped_schedules", result.Skipped, "failed_schedules", result.Failed,
			"notify_failed_schedules", result.NotifyFailed)
	}
}

func (w *Worker) tickReminders(ctx context.Context) {
	if w.notify == nil {
		return
	}
	result, err := w.runner.RunDueReminders(ctx, time.Now().UTC(), w.notify)
	if err != nil {
		w.logger.Error("reminder tick failed", "error", err)
	}
	if len(result.Sent) > 0 || len(result.Retrying) > 0 || len(result.Failed) > 0 {
		w.logger.Info("reminder tick", "sent", result.Sent, "retrying", result.Retrying, "failed", result.Failed)
	}
}

func untilNext(now time.Time, interval time.Duration) time.Duration {
	return now.Truncate(interval).Add(interval).Sub(now)
}
