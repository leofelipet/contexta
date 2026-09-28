package postgres

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/leofelipet/contexta/internal/memories"
	"github.com/leofelipet/contexta/internal/tasks"
)

func TestTaskMemoryCleanup(t *testing.T) {
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

	newTask := func(title string) tasks.Task {
		t.Helper()
		task, err := store.CreateTask(ctx, tasks.CreateParams{Title: title})
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

	t.Run("delete keeps memories by default", func(t *testing.T) {
		task := newTask("default delete")
		memory := newMemory("only on default delete")
		attach(task, memory)

		result, err := store.DeleteTask(ctx, task.ID, false)
		if err != nil || !result.Deleted {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
		if len(result.DeletedMemoryIDs) != 0 || !memoryExists(memory.ID) {
			t.Fatalf("memory should survive: %#v", result)
		}
	})

	t.Run("delete removes exclusive memories and keeps shared ones", func(t *testing.T) {
		task := newTask("delete with memories")
		other := newTask("other task")
		exclusive := newMemory("exclusive memory")
		shared := newMemory("shared memory")
		attach(task, exclusive)
		attach(task, shared)
		attach(other, shared)

		result, err := store.DeleteTask(ctx, task.ID, true)
		if err != nil || !result.Deleted {
			t.Fatalf("result = %#v, err = %v", result, err)
		}
		if !slices.Equal(result.DeletedMemoryIDs, []string{exclusive.ID}) || !slices.Equal(result.KeptMemoryIDs, []string{shared.ID}) {
			t.Fatalf("cleanup = %#v", result.MemoryCleanup)
		}
		if memoryExists(exclusive.ID) || !memoryExists(shared.ID) {
			t.Fatal("wrong memories deleted")
		}
		if _, err := store.GetTask(ctx, task.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("task still exists: %v", err)
		}
	})

	t.Run("delete missing task rolls back memory cleanup", func(t *testing.T) {
		if _, err := store.DeleteTask(ctx, "999999999", true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("update to done removes exclusive memories and keeps shared links", func(t *testing.T) {
		task := newTask("complete with memories")
		other := newTask("other open task")
		exclusive := newMemory("exclusive on completion")
		shared := newMemory("shared on completion")
		attach(task, exclusive)
		attach(task, shared)
		attach(other, shared)

		done := tasks.StatusDone
		updated, cleanup, err := store.UpdateTask(ctx, task.ID, tasks.UpdateParams{Status: &done, DeleteMemories: true})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Status != tasks.StatusDone {
			t.Fatalf("status = %s", updated.Status)
		}
		if !slices.Equal(cleanup.DeletedMemoryIDs, []string{exclusive.ID}) || !slices.Equal(cleanup.KeptMemoryIDs, []string{shared.ID}) {
			t.Fatalf("cleanup = %#v", cleanup)
		}
		if len(updated.Memories) != 1 || updated.Memories[0].ID != shared.ID {
			t.Fatalf("remaining links = %#v", updated.Memories)
		}
		if memoryExists(exclusive.ID) {
			t.Fatal("exclusive memory not deleted")
		}
	})

	t.Run("update rejects delete_memories on open status", func(t *testing.T) {
		task := newTask("still open")
		memory := newMemory("must survive")
		attach(task, memory)

		inProgress := tasks.StatusInProgress
		_, _, err := store.UpdateTask(ctx, task.ID, tasks.UpdateParams{Status: &inProgress, DeleteMemories: true})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("err = %v", err)
		}
		if !memoryExists(memory.ID) {
			t.Fatal("memory deleted on rejected update")
		}
	})
}
