package mcpserver

import (
	"context"
	"errors"

	"github.com/leofelipet/contexta/internal/pushover"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *server) addPushoverTools(mcpServer *mcp.Server) {
	if s.pushover == nil {
		return
	}
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("send_pushover_notification", "Send an external Pushover notification. Requires the Pushover integration to be configured. Emergency priority 2 is not supported."), s.sendPushoverNotification)
}

type sendPushoverNotificationInput struct {
	Message  string `json:"message" jsonschema:"Required notification text."`
	Title    string `json:"title,omitempty" jsonschema:"Optional notification title."`
	Priority *int   `json:"priority,omitempty" jsonschema:"Optional priority from -2 (lowest) to 1 (high); defaults to 0."`
	URL      string `json:"url,omitempty" jsonschema:"Optional supplementary URL."`
}

type sendPushoverNotificationOutput struct {
	Sent    bool   `json:"sent"`
	Request string `json:"request,omitempty"`
}

func (s *server) sendPushoverNotification(ctx context.Context, _ *mcp.CallToolRequest, input sendPushoverNotificationInput) (*mcp.CallToolResult, sendPushoverNotificationOutput, error) {
	s.logAccess(ctx, "send_pushover_notification")
	result, err := s.pushover.Send(ctx, pushover.SendParams{
		Message: input.Message, Title: input.Title, Priority: input.Priority, URL: input.URL,
	})
	if err != nil {
		s.logError(ctx, "send_pushover_notification", err)
		return nil, sendPushoverNotificationOutput{}, safePushoverToolError(err)
	}
	return nil, sendPushoverNotificationOutput{Sent: true, Request: result.Request}, nil
}

func safePushoverToolError(err error) error {
	switch {
	case errors.Is(err, pushover.ErrNotConfigured):
		return errors.New("pushover is not configured")
	case errors.Is(err, pushover.ErrDisabled):
		return errors.New("pushover is disabled")
	case errors.Is(err, pushover.ErrProvider):
		return err
	case errors.Is(err, pushover.ErrInvalidArgument):
		return errors.New("invalid argument")
	case errors.Is(err, pushover.ErrMissingKey):
		return errors.New("email credentials key is not configured")
	default:
		return safeToolError(err)
	}
}
