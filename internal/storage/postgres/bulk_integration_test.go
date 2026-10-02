package postgres

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/leofelipet/contexta/internal/companies"
	"github.com/leofelipet/contexta/internal/memories"
	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/leofelipet/contexta/internal/tasks"
)

func TestBulkOperations(t *testing.T) {
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

	newTask := func(title, status string) tasks.Task {
		t.Helper()
		task, err := store.CreateTask(ctx, tasks.CreateParams{Title: title, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	newMemory := func(content string) memories.Memory {
		t.Helper()
		memory, err := store.CreateMemory(ctx, memories.CreateParams{Content: content}, nil, "", errors.New("no embedding in test"))
		if err != nil {
			t.Fatal(err)
		}
		return memory
	}
	attach := func(task tasks.Task, memory memories.Memory) {
		t.Helper()
		if _, err := store.AttachTaskMemory(ctx, task.ID, memory.ID); err != nil {
			t.Fatal(err)
		}
	}
	memoryExists := func(id string) bool {
		t.Helper()
		_, err := store.GetMemory(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		return true
	}

	t.Run("delete tasks keeps memories shared outside the batch", func(t *testing.T) {
		first, second, outside := newTask("Bulk A", ""), newTask("Bulk B", ""), newTask("Bulk outside", "")
		insideOnly := newMemory("linked to both tasks in the batch")
		shared := newMemory("also linked outside the batch")
		attach(first, insideOnly)
		attach(second, insideOnly)
		attach(second, shared)
		attach(outside, shared)

		result, err := store.DeleteTasks(ctx, []string{first.ID, second.ID, first.ID, "999999999"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if result.Count != 2 {
			t.Fatalf("count = %d, want 2", result.Count)
		}
		if !slices.Equal(result.DeletedMemoryIDs, []string{insideOnly.ID}) || !slices.Equal(result.KeptMemoryIDs, []string{shared.ID}) {
			t.Fatalf("cleanup = %+v", result.MemoryCleanup)
		}
		if memoryExists(insideOnly.ID) || !memoryExists(shared.ID) {
			t.Fatal("memory cleanup did not match the result")
		}
		if _, err := store.GetTask(ctx, outside.ID); err != nil {
			t.Fatalf("task outside the batch: %v", err)
		}
	})

	t.Run("update tasks status sets due_at only on newly done tasks", func(t *testing.T) {
		open, done := newTask("Bulk open", ""), newTask("Bulk done", tasks.StatusDone)
		doneBefore, err := store.GetTask(ctx, done.ID)
		if err != nil {
			t.Fatal(err)
		}
		result, err := store.UpdateTasksStatus(ctx, []string{open.ID, done.ID}, tasks.StatusDone, false)
		if err != nil {
			t.Fatal(err)
		}
		if result.Count != 2 {
			t.Fatalf("count = %d, want 2", result.Count)
		}
		openAfter, _ := store.GetTask(ctx, open.ID)
		doneAfter, _ := store.GetTask(ctx, done.ID)
		if openAfter.Status != tasks.StatusDone || openAfter.DueAt == nil {
			t.Fatalf("open task after = %+v", openAfter)
		}
		if !equalTimes(doneBefore.DueAt, doneAfter.DueAt) {
			t.Fatalf("already-done due_at changed: %v -> %v", doneBefore.DueAt, doneAfter.DueAt)
		}

		if _, err := store.UpdateTasksStatus(ctx, []string{open.ID}, tasks.StatusPending, true); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("delete_memories on an open status: err = %v", err)
		}
		if _, err := store.UpdateTasksStatus(ctx, []string{open.ID}, "archived", false); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid status: err = %v", err)
		}
	})

	t.Run("delete memories", func(t *testing.T) {
		first, second := newMemory("bulk memory 1"), newMemory("bulk memory 2")
		count, err := store.DeleteMemories(ctx, []string{first.ID, second.ID})
		if err != nil || count != 2 {
			t.Fatalf("count = %d, err = %v", count, err)
		}
		if memoryExists(first.ID) || memoryExists(second.ID) {
			t.Fatal("memories still exist")
		}
	})

	t.Run("pause and resume schedules", func(t *testing.T) {
		enabled := true
		schedule, err := store.CreateTaskSchedule(ctx, schedules.CreateParams{Cron: "0 9 * * 1-5", Title: "Bulk schedule", Enabled: &enabled})
		if err != nil {
			t.Fatal(err)
		}
		if count, err := store.SetTaskSchedulesEnabled(ctx, []string{schedule.ID}, false); err != nil || count != 1 {
			t.Fatalf("pause count = %d, err = %v", count, err)
		}
		paused, _ := store.GetTaskSchedule(ctx, schedule.ID)
		if paused.Enabled || paused.NextRunAt != nil {
			t.Fatalf("paused = %+v", paused)
		}
		if _, err := store.SetTaskSchedulesEnabled(ctx, []string{schedule.ID}, true); err != nil {
			t.Fatal(err)
		}
		resumed, _ := store.GetTaskSchedule(ctx, schedule.ID)
		if !resumed.Enabled || resumed.NextRunAt == nil {
			t.Fatalf("resumed = %+v", resumed)
		}
		if count, err := store.DeleteTaskSchedules(ctx, []string{schedule.ID}); err != nil || count != 1 {
			t.Fatalf("delete count = %d, err = %v", count, err)
		}
	})

	t.Run("delete companies keeps linked tasks", func(t *testing.T) {
		company, err := store.CreateCompany(ctx, companies.CreateParams{Name: "Bulk Co " + strconv.FormatInt(int64(os.Getpid()), 10)})
		if err != nil {
			t.Fatal(err)
		}
		task, err := store.CreateTask(ctx, tasks.CreateParams{Title: "Bulk company task", CompanyID: company.ID})
		if err != nil {
			t.Fatal(err)
		}
		if count, err := store.DeleteCompanies(ctx, []string{company.ID}); err != nil || count != 1 {
			t.Fatalf("count = %d, err = %v", count, err)
		}
		after, err := store.GetTask(ctx, task.ID)
		if err != nil || after.CompanyID != "" {
			t.Fatalf("task after = %+v, err = %v", after, err)
		}
	})

	t.Run("rejects empty, oversized, and malformed batches", func(t *testing.T) {
		tooMany := make([]string, maxBulkIDs+1)
		for i := range tooMany {
			tooMany[i] = strconv.Itoa(i + 1)
		}
		cases := map[string]func() error{
			"empty":     func() error { _, err := store.DeleteTasks(ctx, []string{" "}, false); return err },
			"too many":  func() error { _, err := store.DeleteCompanies(ctx, tooMany); return err },
			"bad uuid":  func() error { _, err := store.DeleteMemories(ctx, []string{"nope"}); return err },
			"bad id":    func() error { _, err := store.DeleteTaskSchedules(ctx, []string{"abc"}); return err },
			"no denies": func() error { _, err := store.RemoveDenylistEntries(ctx, nil); return err },
		}
		for name, run := range cases {
			if err := run(); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
}

func equalTimes(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
