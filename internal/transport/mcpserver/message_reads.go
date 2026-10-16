package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/leofelipet/contexta/internal/messages"
	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/leofelipet/contexta/internal/storage/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *server) addMessageReadTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, localWriteTool("acknowledge_messages_bulk", fmt.Sprintf("Mark every unread message matching a filter (time range, conversation, contact, direction) as processed by an agent consumer in one server-side operation, without an ID limit. Use dry_run=true first to count. Filters covering more than %d unread messages require confirm=true. Audio still being transcribed is skipped. Never changes WhatsApp data.", postgres.BulkAcknowledgeConfirmLimit)), s.acknowledgeMessagesBulk)
	mcp.AddTool(mcpServer, readOnlyTool("count_unread", "Count messages not yet acknowledged by an agent consumer, grouped by calendar month."), s.countUnread)
}

type acknowledgeMessagesBulkInput struct {
	ConsumerID     string `json:"consumer_id" jsonschema:"Required stable agent consumer ID, e.g. contexta-bot."`
	Before         string `json:"before" jsonschema:"Required. Acknowledge messages whose timestamp is strictly before this RFC3339 timestamp or YYYY-MM-DD date (start of day, UTC)."`
	After          string `json:"after,omitempty" jsonschema:"Optional inclusive start of the period: RFC3339 timestamp or YYYY-MM-DD date."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Optional Contexta conversation ID."`
	ContactID      string `json:"contact_id,omitempty" jsonschema:"Optional Contexta contact ID: messages sent by the contact or in their direct chat."`
	Direction      string `json:"direction,omitempty" jsonschema:"incoming, outgoing, or all. Defaults to all. inbound and outbound are accepted too."`
	DryRun         bool   `json:"dry_run,omitempty" jsonschema:"Only count what would be acknowledged; write nothing."`
	Confirm        bool   `json:"confirm,omitempty" jsonschema:"Required when the filter covers more unread messages than the safety limit."`
}

type acknowledgeMessagesBulkOutput struct {
	messages.BulkAcknowledgeResult
	Before string `json:"before"`
	After  string `json:"after,omitempty"`
}

func (s *server) acknowledgeMessagesBulk(ctx context.Context, _ *mcp.CallToolRequest, input acknowledgeMessagesBulkInput) (*mcp.CallToolResult, acknowledgeMessagesBulkOutput, error) {
	s.logAccess(ctx, "acknowledge_messages_bulk")
	before, err := parseDateTime(input.Before, false)
	if err != nil || before == nil {
		return nil, acknowledgeMessagesBulkOutput{}, errors.New("invalid before; expected RFC3339 or YYYY-MM-DD")
	}
	after, err := parseDateTime(input.After, false)
	if err != nil {
		return nil, acknowledgeMessagesBulkOutput{}, errors.New("invalid after; expected RFC3339 or YYYY-MM-DD")
	}
	result, err := s.store.AcknowledgeMessagesBulk(ctx, messages.BulkAcknowledgeParams{
		ConsumerID: input.ConsumerID, Before: *before, After: after,
		ConversationID: input.ConversationID, ContactID: input.ContactID, Direction: input.Direction,
		DryRun: input.DryRun, Confirm: input.Confirm,
	})
	if err != nil {
		s.logError(ctx, "acknowledge_messages_bulk", err)
		return nil, acknowledgeMessagesBulkOutput{}, safeToolError(err)
	}
	if result.ConfirmationRequired {
		return nil, acknowledgeMessagesBulkOutput{}, fmt.Errorf("filter covers %d unread messages (limit %d); nothing was acknowledged. Repeat with confirm=true", result.Matched, postgres.BulkAcknowledgeConfirmLimit)
	}
	output := acknowledgeMessagesBulkOutput{BulkAcknowledgeResult: result, Before: before.Format(time.RFC3339)}
	if after != nil {
		output.After = after.Format(time.RFC3339)
	}
	return nil, output, nil
}

type countUnreadInput struct {
	ConsumerID string `json:"consumer_id" jsonschema:"Required stable agent consumer ID, e.g. contexta-bot."`
	Before     string `json:"before,omitempty" jsonschema:"Optional exclusive end: RFC3339 timestamp or YYYY-MM-DD date (start of day, UTC)."`
	After      string `json:"after,omitempty" jsonschema:"Optional inclusive start: RFC3339 timestamp or YYYY-MM-DD date."`
	Timezone   string `json:"timezone,omitempty" jsonschema:"IANA timezone for month boundaries. Defaults to America/Sao_Paulo."`
}

func (s *server) countUnread(ctx context.Context, _ *mcp.CallToolRequest, input countUnreadInput) (*mcp.CallToolResult, messages.UnreadCount, error) {
	s.logAccess(ctx, "count_unread")
	before, err := parseDateTime(input.Before, false)
	if err != nil {
		return nil, messages.UnreadCount{}, errors.New("invalid before; expected RFC3339 or YYYY-MM-DD")
	}
	after, err := parseDateTime(input.After, false)
	if err != nil {
		return nil, messages.UnreadCount{}, errors.New("invalid after; expected RFC3339 or YYYY-MM-DD")
	}
	timezone := input.Timezone
	if timezone == "" {
		timezone = schedules.DefaultTimezone
	}
	count, err := s.store.CountUnreadMessages(ctx, messages.UnreadCountParams{
		ConsumerID: input.ConsumerID, Before: before, After: after, Timezone: timezone,
	})
	if err != nil {
		s.logError(ctx, "count_unread", err)
		return nil, messages.UnreadCount{}, safeToolError(err)
	}
	return nil, count, nil
}
