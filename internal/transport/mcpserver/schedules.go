package mcpserver

import (
	"context"
	"errors"

	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const cronHelp = "Standard 5-field cron (minute hour day-of-month month day-of-week), e.g. '0 9 * * 1-5' for weekdays at 09:00, or descriptors like @daily/@weekly/@monthly. The scheduler ticks every 5 minutes, so expressions firing more often than every 5 minutes are rejected and off-tick minutes run on the next tick."

func (s *server) addScheduleTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, readOnlyTool("list_task_schedules", "List recurring task schedules with optional enabled filter and text query (matches title, description, or numeric ID)."), s.listTaskSchedules)
	mcp.AddTool(mcpServer, readOnlyTool("get_task_schedule", "Get one recurring task schedule by numeric ID, including next_run_at, last_run_at, and last_task_id."), s.getTaskSchedule)
	mcp.AddTool(mcpServer, localWriteTool("create_task_schedule", "Create a recurring task schedule: on each cron occurrence a new pending task is created from this template. "+cronHelp), s.createTaskSchedule)
	mcp.AddTool(mcpServer, localWriteTool("update_task_schedule", "Update a recurring task schedule. Changing cron or timezone, or re-enabling, recomputes next_run_at. Set enabled=false to pause. Already created tasks are not changed."), s.updateTaskSchedule)
	mcp.AddTool(mcpServer, localWriteTool("delete_task_schedule", "Permanently delete a recurring task schedule. Tasks it already created are kept."), s.deleteTaskSchedule)
}

type listTaskSchedulesInput struct {
	Enabled   *bool  `json:"enabled,omitempty" jsonschema:"When set, only return enabled (true) or paused (false) schedules."`
	CompanyID string `json:"company_id,omitempty" jsonschema:"Only return schedules linked to this numeric company ID."`
	Query     string `json:"query,omitempty" jsonschema:"Text matched against title and description, or an exact numeric schedule ID."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum number of schedules, up to 100."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type taskSchedulesOutput struct {
	Schedules  []schedules.Schedule `json:"schedules"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func (s *server) listTaskSchedules(ctx context.Context, _ *mcp.CallToolRequest, input listTaskSchedulesInput) (*mcp.CallToolResult, taskSchedulesOutput, error) {
	s.logAccess(ctx, "list_task_schedules")
	page, err := s.store.ListTaskSchedules(ctx, schedules.ListParams{
		Enabled: input.Enabled, CompanyID: input.CompanyID, Query: input.Query, Limit: mcpLimit(input.Limit), Cursor: input.Cursor,
	})
	if err != nil {
		s.logError(ctx, "list_task_schedules", err)
		return nil, taskSchedulesOutput{}, safeScheduleToolError(err)
	}
	return nil, taskSchedulesOutput{Schedules: page.Schedules, NextCursor: page.NextCursor}, nil
}

type taskScheduleIDInput struct {
	ID string `json:"id" jsonschema:"Required numeric task schedule ID (e.g. 1, 2, 3)."`
}

type taskScheduleOutput struct {
	Schedule schedules.Schedule `json:"schedule"`
}

func (s *server) getTaskSchedule(ctx context.Context, _ *mcp.CallToolRequest, input taskScheduleIDInput) (*mcp.CallToolResult, taskScheduleOutput, error) {
	s.logAccess(ctx, "get_task_schedule")
	schedule, err := s.store.GetTaskSchedule(ctx, input.ID)
	if err != nil {
		s.logError(ctx, "get_task_schedule", err)
		return nil, taskScheduleOutput{}, safeScheduleToolError(err)
	}
	return nil, taskScheduleOutput{Schedule: schedule}, nil
}

type createTaskScheduleInput struct {
	Cron           string `json:"cron" jsonschema:"Required cron expression. Standard 5 fields or @daily/@weekly/@monthly/@yearly."`
	Timezone       string `json:"timezone,omitempty" jsonschema:"IANA timezone used to evaluate cron, e.g. America/Sao_Paulo (default)."`
	Title          string `json:"title" jsonschema:"Required title of each created task."`
	Description    string `json:"description,omitempty" jsonschema:"Optional description of each created task."`
	CompanyID      string `json:"company_id,omitempty" jsonschema:"Optional numeric company ID linked to each created task."`
	DueInMinutes   *int   `json:"due_in_minutes,omitempty" jsonschema:"Optional: created tasks get due_at = creation time + this many minutes (e.g. 480 for 8 hours)."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Optional Contexta conversation UUID to link on each created task."`
	ContactID      string `json:"contact_id,omitempty" jsonschema:"Optional Contexta contact UUID to link on each created task."`
	SkipIfOpen     bool   `json:"skip_if_open,omitempty" jsonschema:"When true, skip an occurrence if the task created by the previous run is still open (pending, in_progress, or blocked)."`
	Enabled        *bool  `json:"enabled,omitempty" jsonschema:"Defaults to true. Set false to create the schedule paused."`
}

func (s *server) createTaskSchedule(ctx context.Context, _ *mcp.CallToolRequest, input createTaskScheduleInput) (*mcp.CallToolResult, taskScheduleOutput, error) {
	s.logAccess(ctx, "create_task_schedule")
	schedule, err := s.store.CreateTaskSchedule(ctx, schedules.CreateParams{
		Cron: input.Cron, Timezone: input.Timezone, Enabled: input.Enabled, SkipIfOpen: input.SkipIfOpen,
		Title: input.Title, Description: input.Description, CompanyID: input.CompanyID,
		DueInMinutes: input.DueInMinutes, ConversationID: input.ConversationID, ContactID: input.ContactID,
	})
	if err != nil {
		s.logError(ctx, "create_task_schedule", err)
		return nil, taskScheduleOutput{}, safeScheduleToolError(err)
	}
	return nil, taskScheduleOutput{Schedule: schedule}, nil
}

type updateTaskScheduleInput struct {
	ID             string  `json:"id" jsonschema:"Required numeric task schedule ID (e.g. 1, 2, 3)."`
	Cron           *string `json:"cron,omitempty" jsonschema:"New cron expression."`
	Timezone       *string `json:"timezone,omitempty" jsonschema:"New IANA timezone. Empty string resets to America/Sao_Paulo."`
	Title          *string `json:"title,omitempty" jsonschema:"New task title."`
	Description    *string `json:"description,omitempty" jsonschema:"New task description."`
	CompanyID      *string `json:"company_id,omitempty" jsonschema:"Linked numeric company ID. Empty string clears it."`
	DueInMinutes   *int    `json:"due_in_minutes,omitempty" jsonschema:"New due offset in minutes. 0 clears it."`
	ConversationID *string `json:"conversation_id,omitempty" jsonschema:"Linked conversation UUID. Empty string clears it."`
	ContactID      *string `json:"contact_id,omitempty" jsonschema:"Linked contact UUID. Empty string clears it."`
	SkipIfOpen     *bool   `json:"skip_if_open,omitempty" jsonschema:"Skip occurrences while the previous task is still open."`
	Enabled        *bool   `json:"enabled,omitempty" jsonschema:"false pauses the schedule; true resumes it from the next occurrence."`
}

func (s *server) updateTaskSchedule(ctx context.Context, _ *mcp.CallToolRequest, input updateTaskScheduleInput) (*mcp.CallToolResult, taskScheduleOutput, error) {
	s.logAccess(ctx, "update_task_schedule")
	schedule, err := s.store.UpdateTaskSchedule(ctx, input.ID, schedules.UpdateParams{
		Cron: input.Cron, Timezone: input.Timezone, Enabled: input.Enabled, SkipIfOpen: input.SkipIfOpen,
		Title: input.Title, Description: input.Description, CompanyID: input.CompanyID,
		DueInMinutes: input.DueInMinutes, ConversationID: input.ConversationID, ContactID: input.ContactID,
	})
	if err != nil {
		s.logError(ctx, "update_task_schedule", err)
		return nil, taskScheduleOutput{}, safeScheduleToolError(err)
	}
	return nil, taskScheduleOutput{Schedule: schedule}, nil
}

type deleteTaskScheduleOutput struct {
	Deleted bool `json:"deleted"`
}

func (s *server) deleteTaskSchedule(ctx context.Context, _ *mcp.CallToolRequest, input taskScheduleIDInput) (*mcp.CallToolResult, deleteTaskScheduleOutput, error) {
	s.logAccess(ctx, "delete_task_schedule")
	if err := s.store.DeleteTaskSchedule(ctx, input.ID); err != nil {
		s.logError(ctx, "delete_task_schedule", err)
		return nil, deleteTaskScheduleOutput{}, safeScheduleToolError(err)
	}
	return nil, deleteTaskScheduleOutput{Deleted: true}, nil
}

// safeScheduleToolError surfaces cron validation details so the agent can fix
// the expression; everything else goes through the generic mapping.
func safeScheduleToolError(err error) error {
	if errors.Is(err, schedules.ErrInvalidCron) {
		return err
	}
	return safeToolError(err)
}
