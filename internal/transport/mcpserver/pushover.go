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
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("send_pushover_notification", "Send an external Pushover notification. Requires the Pushover integration to be configured. Emergency priority 2 repeats the alert every retry seconds until acknowledged or expire seconds elapse, and returns a receipt."), s.sendPushoverNotification)
	s.addReminderTools(mcpServer)
	mcp.AddTool(mcpServer, openWorldReadTool("list_pushover_sounds", "List Pushover notification sounds (built-in and custom) usable as the sound of send_pushover_notification."), s.listPushoverSounds)
}

type sendPushoverNotificationInput struct {
	Message  string `json:"message" jsonschema:"Required notification text."`
	Title    string `json:"title,omitempty" jsonschema:"Optional notification title."`
	Priority *int   `json:"priority,omitempty" jsonschema:"Optional priority from -2 (lowest) to 2 (emergency); defaults to 0. Emergency priority 2 repeats until acknowledged or expired."`
	URL      string `json:"url,omitempty" jsonschema:"Optional supplementary URL."`
	Sound    string `json:"sound,omitempty" jsonschema:"Optional sound id from list_pushover_sounds (e.g. pushover, siren, none); omit to use the recipient's default tone."`
	Retry    *int   `json:"retry,omitempty" jsonschema:"Optional seconds between emergency retries, minimum 30; defaults to 60. Only allowed with priority 2."`
	Expire   *int   `json:"expire,omitempty" jsonschema:"Optional seconds until emergency retries stop, up to 10800 and not less than retry; defaults to 3600. Only allowed with priority 2."`
}

type sendPushoverNotificationOutput struct {
	Sent    bool   `json:"sent"`
	Request string `json:"request,omitempty"`
	Receipt string `json:"receipt,omitempty"`
}

func (s *server) sendPushoverNotification(ctx context.Context, _ *mcp.CallToolRequest, input sendPushoverNotificationInput) (*mcp.CallToolResult, sendPushoverNotificationOutput, error) {
	s.logAccess(ctx, "send_pushover_notification")
	result, err := s.pushover.Send(ctx, pushover.SendParams{
		Message: input.Message, Title: input.Title, Priority: input.Priority, URL: input.URL,
		Sound: input.Sound, Retry: input.Retry, Expire: input.Expire,
	})
	if err != nil {
		s.logError(ctx, "send_pushover_notification", err)
		return nil, sendPushoverNotificationOutput{}, safePushoverToolError(err)
	}
	return nil, sendPushoverNotificationOutput{Sent: true, Request: result.Request, Receipt: result.Receipt}, nil
}

type listPushoverSoundsInput struct{}

type listPushoverSoundsOutput struct {
	Sounds []pushover.Sound `json:"sounds"`
}

func (s *server) listPushoverSounds(ctx context.Context, _ *mcp.CallToolRequest, _ listPushoverSoundsInput) (*mcp.CallToolResult, listPushoverSoundsOutput, error) {
	s.logAccess(ctx, "list_pushover_sounds")
	sounds, err := s.pushover.Sounds(ctx)
	if err != nil {
		s.logError(ctx, "list_pushover_sounds", err)
		return nil, listPushoverSoundsOutput{}, safePushoverToolError(err)
	}
	return nil, listPushoverSoundsOutput{Sounds: sounds}, nil
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
