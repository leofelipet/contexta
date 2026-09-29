package schedules

import (
	"context"
	"log/slog"
	"time"
)

type Runner interface {
	RunDueTaskSchedules(ctx context.Context, now time.Time) (RunResult, error)
}

type Worker struct {
	runner Runner
	logger *slog.Logger
}

func NewWorker(runner Runner, logger *slog.Logger) *Worker {
	return &Worker{runner: runner, logger: logger}
}

// Run fires once at startup (to catch up on runs missed while down) and then
// on every wall-clock multiple of TickInterval (:00, :05, :10…).
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		w.tick(ctx)
		timer.Reset(untilNextTick(time.Now()))
	}
}

func (w *Worker) tick(ctx context.Context) {
	result, err := w.runner.RunDueTaskSchedules(ctx, time.Now().UTC())
	if err != nil {
		w.logger.Error("task scheduler tick failed", "error", err)
	}
	if len(result.Created) > 0 || len(result.Skipped) > 0 || len(result.Failed) > 0 {
		w.logger.Info("task scheduler tick",
			"created_tasks", result.Created, "skipped_schedules", result.Skipped, "failed_schedules", result.Failed)
	}
}

func untilNextTick(now time.Time) time.Duration {
	return now.Truncate(TickInterval).Add(TickInterval).Sub(now)
}
