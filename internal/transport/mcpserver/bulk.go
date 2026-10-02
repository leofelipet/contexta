package mcpserver

import (
	"context"
	"errors"

	"github.com/leofelipet/contexta/internal/tasks"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *server) addBulkTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, localWriteTool("delete_tasks", "Permanently delete up to 100 tasks by ID in one call. IDs that do not exist are ignored. With delete_memories=true, also permanently deletes memories linked only to tasks in this batch; memories linked to other tasks are kept."), s.deleteTasks)
	mcp.AddTool(mcpServer, localWriteTool("update_tasks_status", "Set the status of up to 100 tasks in one call. Setting status to done sets due_at to now on tasks not already done. When closing (done or cancelled), delete_memories=true permanently deletes memories linked only to tasks in this batch."), s.updateTasksStatus)
	mcp.AddTool(mcpServer, localWriteTool("delete_memories", "Permanently delete up to 100 memories by ID in one call. IDs that do not exist are ignored."), s.deleteMemories)
	mcp.AddTool(mcpServer, localWriteTool("delete_task_schedules", "Permanently delete up to 100 recurring task schedules by ID in one call. Tasks they already created are kept."), s.deleteTaskSchedules)
	mcp.AddTool(mcpServer, localWriteTool("set_task_schedules_enabled", "Pause (enabled=false) or resume (enabled=true) up to 100 recurring task schedules in one call. Resumed schedules run next at their next cron time."), s.setTaskSchedulesEnabled)
	mcp.AddTool(mcpServer, localWriteTool("delete_companies", "Permanently delete up to 100 companies by ID in one call. Linked tasks, schedules, and contacts are kept without the company link."), s.deleteCompanies)
	mcp.AddTool(mcpServer, localWriteTool("remove_denylist_entries", "Remove up to 100 denylist entries by ID in one call so messages from those targets are ingested again."), s.removeDenylistEntries)
}

type bulkIDsInput struct {
	IDs []string `json:"ids" jsonschema:"One to 100 IDs."`
}

type bulkCountOutput struct {
	Count int `json:"count"`
}

type deleteTasksInput struct {
	IDs            []string `json:"ids" jsonschema:"One to 100 numeric Contexta task IDs."`
	DeleteMemories bool     `json:"delete_memories,omitempty" jsonschema:"Also permanently delete memories linked only to tasks in this batch. Defaults to false."`
}

func (s *server) deleteTasks(ctx context.Context, _ *mcp.CallToolRequest, input deleteTasksInput) (*mcp.CallToolResult, tasks.BulkResult, error) {
	s.logAccess(ctx, "delete_tasks")
	result, err := s.store.DeleteTasks(ctx, input.IDs, input.DeleteMemories)
	if err != nil {
		s.logError(ctx, "delete_tasks", err)
		return nil, tasks.BulkResult{}, safeToolError(err)
	}
	return nil, result, nil
}

type updateTasksStatusInput struct {
	IDs            []string `json:"ids" jsonschema:"One to 100 numeric Contexta task IDs."`
	Status         string   `json:"status" jsonschema:"pending, in_progress, blocked, done, or cancelled."`
	DeleteMemories bool     `json:"delete_memories,omitempty" jsonschema:"Only with status done or cancelled: permanently delete memories linked only to tasks in this batch. Defaults to false."`
}

func (s *server) updateTasksStatus(ctx context.Context, _ *mcp.CallToolRequest, input updateTasksStatusInput) (*mcp.CallToolResult, tasks.BulkResult, error) {
	s.logAccess(ctx, "update_tasks_status")
	result, err := s.store.UpdateTasksStatus(ctx, input.IDs, input.Status, input.DeleteMemories)
	if err != nil {
		s.logError(ctx, "update_tasks_status", err)
		return nil, tasks.BulkResult{}, safeToolError(err)
	}
	return nil, result, nil
}

func (s *server) deleteMemories(ctx context.Context, _ *mcp.CallToolRequest, input bulkIDsInput) (*mcp.CallToolResult, bulkCountOutput, error) {
	s.logAccess(ctx, "delete_memories")
	if s.memories == nil {
		return nil, bulkCountOutput{}, errors.New("memories unavailable")
	}
	count, err := s.memories.DeleteMany(ctx, input.IDs)
	if err != nil {
		s.logError(ctx, "delete_memories", err)
		return nil, bulkCountOutput{}, safeToolError(err)
	}
	return nil, bulkCountOutput{Count: count}, nil
}

func (s *server) deleteTaskSchedules(ctx context.Context, _ *mcp.CallToolRequest, input bulkIDsInput) (*mcp.CallToolResult, bulkCountOutput, error) {
	s.logAccess(ctx, "delete_task_schedules")
	count, err := s.store.DeleteTaskSchedules(ctx, input.IDs)
	if err != nil {
		s.logError(ctx, "delete_task_schedules", err)
		return nil, bulkCountOutput{}, safeToolError(err)
	}
	return nil, bulkCountOutput{Count: count}, nil
}

type setTaskSchedulesEnabledInput struct {
	IDs     []string `json:"ids" jsonschema:"One to 100 numeric schedule IDs."`
	Enabled bool     `json:"enabled" jsonschema:"true resumes the schedules, false pauses them."`
}

func (s *server) setTaskSchedulesEnabled(ctx context.Context, _ *mcp.CallToolRequest, input setTaskSchedulesEnabledInput) (*mcp.CallToolResult, bulkCountOutput, error) {
	s.logAccess(ctx, "set_task_schedules_enabled")
	count, err := s.store.SetTaskSchedulesEnabled(ctx, input.IDs, input.Enabled)
	if err != nil {
		s.logError(ctx, "set_task_schedules_enabled", err)
		return nil, bulkCountOutput{}, safeToolError(err)
	}
	return nil, bulkCountOutput{Count: count}, nil
}

func (s *server) deleteCompanies(ctx context.Context, _ *mcp.CallToolRequest, input bulkIDsInput) (*mcp.CallToolResult, bulkCountOutput, error) {
	s.logAccess(ctx, "delete_companies")
	count, err := s.store.DeleteCompanies(ctx, input.IDs)
	if err != nil {
		s.logError(ctx, "delete_companies", err)
		return nil, bulkCountOutput{}, safeToolError(err)
	}
	return nil, bulkCountOutput{Count: count}, nil
}

func (s *server) removeDenylistEntries(ctx context.Context, _ *mcp.CallToolRequest, input bulkIDsInput) (*mcp.CallToolResult, bulkCountOutput, error) {
	s.logAccess(ctx, "remove_denylist_entries")
	count, err := s.store.RemoveDenylistEntries(ctx, input.IDs)
	if err != nil {
		s.logError(ctx, "remove_denylist_entries", err)
		return nil, bulkCountOutput{}, safeToolError(err)
	}
	return nil, bulkCountOutput{Count: count}, nil
}
