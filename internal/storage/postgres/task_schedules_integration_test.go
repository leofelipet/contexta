package postgres

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/leofelipet/contexta/internal/tasks"
)

func TestTaskSchedules(t *testing.T) {
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

	// Forces a schedule to be due without waiting for its cron.
	makeDue := func(id string) {
		t.Helper()
		if _, err := store.pool.Exec(ctx, `UPDATE task_schedules SET next_run_at = now() - interval '1 minute' WHERE id = $1::bigint`, id); err != nil {
			t.Fatal(err)
		}
	}
	runNow := func() schedules.RunResult {
		t.Helper()
		result, err := store.RunDueTaskSchedules(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	t.Run("create validates cron and computes next run", func(t *testing.T) {
		if _, err := store.CreateTaskSchedule(ctx, schedules.CreateParams{Cron: "* * * * *", Title: "too often"}); !errors.Is(err, ErrInvalidArgument) || !errors.Is(err, schedules.ErrInvalidCron) {
			t.Fatalf("err = %v, want invalid cron", err)
		}
		schedule, err := store.CreateTaskSchedule(ctx, schedules.CreateParams{Cron: "0 9 * * *", Title: "  Daily review  "})
		if err != nil {
			t.Fatal(err)
		}
		if schedule.Title != "Daily review" || schedule.Timezone != schedules.DefaultTimezone || !schedule.Enabled {
			t.Fatalf("schedule = %#v", schedule)
		}
		if schedule.NextRunAt == nil || !schedule.NextRunAt.After(time.Now()) {
			t.Fatalf("next_run_at = %v", schedule.NextRunAt)
		}
	})

	t.Run("due schedule creates one task and advances", func(t *testing.T) {
		due := 60
		schedule, err := store.CreateTaskSchedule(ctx, schedules.CreateParams{
			Cron: "0 9 * * *", Title: "Scheduled", Company: "ACME", DueInMinutes: &due,
		})
		if err != nil {
			t.Fatal(err)
		}
		makeDue(schedule.ID)

		result := runNow()
		if len(result.Created) != 1 {
			t.Fatalf("result = %#v", result)
		}
		task, err := store.GetTask(ctx, result.Created[0])
		if err != nil {
			t.Fatal(err)
		}
		if task.Title != "Scheduled" || task.Company != "ACME" || task.Status != tasks.StatusPending || task.ScheduleID != schedule.ID || task.DueAt == nil {
			t.Fatalf("task = %#v", task)
		}

		updated, err := store.GetTaskSchedule(ctx, schedule.ID)
		if err != nil {
			t.Fatal(err)
		}
		if updated.RunCount != 1 || updated.LastTaskID != task.ID || updated.LastRunAt == nil || !updated.NextRunAt.After(time.Now()) {
			t.Fatalf("schedule after run = %#v", updated)
		}

		// Nothing due anymore: a second tick is a no-op for this schedule.
		if result := runNow(); slices.Contains(result.Created, task.ID) || len(result.Created) != 0 {
			t.Fatalf("second tick = %#v", result)
		}

		page, err := store.ListTasks(ctx, tasks.ListParams{ScheduleID: schedule.ID})
		if err != nil || len(page.Tasks) != 1 {
			t.Fatalf("tasks by schedule = %#v, err = %v", page.Tasks, err)
		}
	})

	t.Run("skip_if_open skips while previous task is open", func(t *testing.T) {
		schedule, err := store.CreateTaskSchedule(ctx, schedules.CreateParams{Cron: "@daily", Title: "Only one open", SkipIfOpen: true})
		if err != nil {
			t.Fatal(err)
		}
		makeDue(schedule.ID)
		first := runNow()
		if len(first.Created) != 1 {
			t.Fatalf("first = %#v", first)
		}

		makeDue(schedule.ID)
		if second := runNow(); !slices.Contains(second.Skipped, schedule.ID) || len(second.Created) != 0 {
			t.Fatalf("second = %#v", second)
		}

		done := tasks.StatusDone
		if _, _, err := store.UpdateTask(ctx, first.Created[0], tasks.UpdateParams{Status: &done}); err != nil {
			t.Fatal(err)
		}
		makeDue(schedule.ID)
		if third := runNow(); len(third.Created) != 1 {
			t.Fatalf("third = %#v", third)
		}
	})

	t.Run("paused schedule never fires and resume recomputes", func(t *testing.T) {
		disabled := false
		schedule, err := store.CreateTaskSchedule(ctx, schedules.CreateParams{Cron: "@hourly", Title: "Paused", Enabled: &disabled})
		if err != nil {
			t.Fatal(err)
		}
		if schedule.NextRunAt != nil {
			t.Fatalf("paused next_run_at = %v", schedule.NextRunAt)
		}
		if result := runNow(); len(result.Created) != 0 {
			t.Fatalf("paused tick = %#v", result)
		}

		enabled := true
		resumed, err := store.UpdateTaskSchedule(ctx, schedule.ID, schedules.UpdateParams{Enabled: &enabled})
		if err != nil {
			t.Fatal(err)
		}
		if resumed.NextRunAt == nil || !resumed.NextRunAt.After(time.Now()) {
			t.Fatalf("resumed = %#v", resumed)
		}

		// A title-only edit keeps a pending due run instead of pushing it forward.
		makeDue(schedule.ID)
		title := "Renamed"
		renamed, err := store.UpdateTaskSchedule(ctx, schedule.ID, schedules.UpdateParams{Title: &title})
		if err != nil {
			t.Fatal(err)
		}
		if renamed.NextRunAt == nil || renamed.NextRunAt.After(time.Now()) {
			t.Fatalf("title edit moved next_run_at: %v", renamed.NextRunAt)
		}
		result := runNow()
		if len(result.Created) != 1 {
			t.Fatalf("renamed tick = %#v", result)
		}
		if task, err := store.GetTask(ctx, result.Created[0]); err != nil || task.Title != "Renamed" {
			t.Fatalf("task = %#v, err = %v", task, err)
		}
	})

	t.Run("concurrent ticks fire once", func(t *testing.T) {
		schedule, err := store.CreateTaskSchedule(ctx, schedules.CreateParams{Cron: "@daily", Title: "Concurrent"})
		if err != nil {
			t.Fatal(err)
		}
		makeDue(schedule.ID)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if _, err := store.RunDueTaskSchedules(ctx, time.Now().UTC()); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		page, err := store.ListTasks(ctx, tasks.ListParams{ScheduleID: schedule.ID})
		if err != nil || len(page.Tasks) != 1 {
			t.Fatalf("tasks = %d, err = %v", len(page.Tasks), err)
		}
	})

	t.Run("delete keeps created tasks", func(t *testing.T) {
		schedule, err := store.CreateTaskSchedule(ctx, schedules.CreateParams{Cron: "@daily", Title: "To delete"})
		if err != nil {
			t.Fatal(err)
		}
		makeDue(schedule.ID)
		result := runNow()
		if len(result.Created) != 1 {
			t.Fatalf("result = %#v", result)
		}
		if err := store.DeleteTaskSchedule(ctx, schedule.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetTaskSchedule(ctx, schedule.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("get deleted = %v", err)
		}
		task, err := store.GetTask(ctx, result.Created[0])
		if err != nil || task.ScheduleID != "" {
			t.Fatalf("task = %#v, err = %v", task, err)
		}
	})
}
