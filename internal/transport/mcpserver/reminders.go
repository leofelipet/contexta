package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/leofelipet/contexta/internal/storage/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const sendAtHelp = "send_at is RFC3339 with offset (e.g. 2026-10-19T13:00:00-03:00) or a local date-time (2026-10-19T13:00) interpreted in timezone. Reminders are delivered within about a minute of send_at."

func (s *server) addReminderTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, readOnlyTool("list_reminders", "List one-off Pushover reminders, newest send_at first, optionally filtered by status (pending, sent, failed)."), s.listReminders)
	mcp.AddTool(mcpServer, readOnlyTool("get_reminder", "Get one Pushover reminder by numeric ID, including status, attempts, and last_error."), s.getReminder)
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("create_reminder", "Schedule a one-off Pushover notification, e.g. 'remind me to go to the bathroom at 13:00'. Failed deliveries are retried up to 3 times. For recurring notifications use create_task_schedule with notify=true. "+sendAtHelp), s.createReminder)
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("update_reminder", "Change a pending Pushover reminder. Sent or failed reminders cannot be changed. "+sendAtHelp), s.updateReminder)
	mcp.AddTool(mcpServer, localWriteTool("delete_reminder", "Delete a Pushover reminder; deleting a pending reminder cancels it."), s.deleteReminder)
}

type listRemindersInput struct {
	Status string `json:"status,omitempty" jsonschema:"Optional status filter: pending, sent, or failed."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of reminders, up to 100."`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type remindersOutput struct {
	Reminders  []schedules.Reminder `json:"reminders"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func (s *server) listReminders(ctx context.Context, _ *mcp.CallToolRequest, input listRemindersInput) (*mcp.CallToolResult, remindersOutput, error) {
	s.logAccess(ctx, "list_reminders")
	page, err := s.store.ListReminders(ctx, schedules.ReminderListParams{Status: input.Status, Limit: mcpLimit(input.Limit), Cursor: input.Cursor})
	if err != nil {
		s.logError(ctx, "list_reminders", err)
		return nil, remindersOutput{}, safeReminderToolError(err)
	}
	return nil, remindersOutput{Reminders: page.Reminders, NextCursor: page.NextCursor}, nil
}

type reminderIDInput struct {
	ID string `json:"id" jsonschema:"Required numeric reminder ID."`
}

type reminderOutput struct {
	Reminder schedules.Reminder `json:"reminder"`
}

func (s *server) getReminder(ctx context.Context, _ *mcp.CallToolRequest, input reminderIDInput) (*mcp.CallToolResult, reminderOutput, error) {
	s.logAccess(ctx, "get_reminder")
	reminder, err := s.store.GetReminder(ctx, input.ID)
	if err != nil {
		s.logError(ctx, "get_reminder", err)
		return nil, reminderOutput{}, safeReminderToolError(err)
	}
	return nil, reminderOutput{Reminder: reminder}, nil
}

type createReminderInput struct {
	Message  string `json:"message" jsonschema:"Required notification text, up to 1024 characters."`
	SendAt   string `json:"send_at" jsonschema:"Required delivery time: RFC3339 with offset or local YYYY-MM-DDTHH:MM in timezone."`
	Timezone string `json:"timezone,omitempty" jsonschema:"IANA timezone for a local send_at, e.g. America/Sao_Paulo (default)."`
	Title    string `json:"title,omitempty" jsonschema:"Optional notification title."`
	Priority *int   `json:"priority,omitempty" jsonschema:"Optional priority from -2 (lowest) to 2 (emergency, repeats until acknowledged); defaults to 0."`
	Sound    string `json:"sound,omitempty" jsonschema:"Optional sound id from list_pushover_sounds; omit to use the recipient's default tone."`
}

func (s *server) createReminder(ctx context.Context, _ *mcp.CallToolRequest, input createReminderInput) (*mcp.CallToolResult, reminderOutput, error) {
	s.logAccess(ctx, "create_reminder")
	sendAt, err := schedules.ParseSendAt(input.SendAt, input.Timezone)
	if err != nil {
		return nil, reminderOutput{}, err
	}
	reminder, err := s.store.CreateReminder(ctx, schedules.ReminderCreateParams{
		Title: input.Title, Message: input.Message, Priority: input.Priority, Sound: input.Sound, SendAt: sendAt,
	})
	if err != nil {
		s.logError(ctx, "create_reminder", err)
		return nil, reminderOutput{}, safeReminderToolError(err)
	}
	return nil, reminderOutput{Reminder: reminder}, nil
}

type updateReminderInput struct {
	ID       string  `json:"id" jsonschema:"Required numeric reminder ID."`
	Message  *string `json:"message,omitempty" jsonschema:"New notification text."`
	SendAt   *string `json:"send_at,omitempty" jsonschema:"New delivery time: RFC3339 with offset or local YYYY-MM-DDTHH:MM in timezone."`
	Timezone string  `json:"timezone,omitempty" jsonschema:"IANA timezone for a local send_at (default America/Sao_Paulo)."`
	Title    *string `json:"title,omitempty" jsonschema:"New title. Empty string clears it."`
	Priority *int    `json:"priority,omitempty" jsonschema:"New priority from -2 to 2."`
	Sound    *string `json:"sound,omitempty" jsonschema:"New sound id. Empty string uses the recipient's default tone."`
}

func (s *server) updateReminder(ctx context.Context, _ *mcp.CallToolRequest, input updateReminderInput) (*mcp.CallToolResult, reminderOutput, error) {
	s.logAccess(ctx, "update_reminder")
	var sendAt *time.Time
	if input.SendAt != nil {
		parsed, err := schedules.ParseSendAt(*input.SendAt, input.Timezone)
		if err != nil {
			return nil, reminderOutput{}, err
		}
		sendAt = &parsed
	}
	reminder, err := s.store.UpdateReminder(ctx, input.ID, schedules.ReminderUpdateParams{
		Title: input.Title, Message: input.Message, Priority: input.Priority, Sound: input.Sound, SendAt: sendAt,
	})
	if err != nil {
		s.logError(ctx, "update_reminder", err)
		return nil, reminderOutput{}, safeReminderToolError(err)
	}
	return nil, reminderOutput{Reminder: reminder}, nil
}

type deleteReminderOutput struct {
	Deleted bool `json:"deleted"`
}

func (s *server) deleteReminder(ctx context.Context, _ *mcp.CallToolRequest, input reminderIDInput) (*mcp.CallToolResult, deleteReminderOutput, error) {
	s.logAccess(ctx, "delete_reminder")
	if err := s.store.DeleteReminder(ctx, input.ID); err != nil {
		s.logError(ctx, "delete_reminder", err)
		return nil, deleteReminderOutput{}, safeReminderToolError(err)
	}
	return nil, deleteReminderOutput{Deleted: true}, nil
}

// safeReminderToolError surfaces validation and state details so the agent can
// fix its input; everything else goes through the generic mapping.
func safeReminderToolError(err error) error {
	if errors.Is(err, schedules.ErrInvalidNotification) {
		return err
	}
	if errors.Is(err, postgres.ErrConflict) {
		return errors.New("only pending reminders can be changed")
	}
	return safeToolError(err)
}
