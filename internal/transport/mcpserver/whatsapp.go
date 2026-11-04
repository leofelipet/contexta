package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/whatsappsend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	whatsAppApproval    = "Requires the user's explicit approval of the recipient and the exact content before sending; any change requires approval again. "
	whatsAppRecipient   = "recipient: phone number in international format (e.g. 5511999999999), a <digits>@s.whatsapp.net JID, or a group JID ending in @g.us (find it with find_conversations); @lid and channels are rejected. "
	whatsAppMentionsDoc = "mentions (groups only): phone numbers to tag, or [\"all\"] to tag every member; include @<number> in the text for the tag to show highlighted. "
	whatsAppDestructive = "Destructive: delivers a message to external people and cannot be undone."

	sendWhatsAppDescription = "Send one WhatsApp text message to one contact or group via UAZAPI. " + whatsAppApproval + whatsAppRecipient +
		"text: 1-4096 characters, sent exactly as given; {{placeholders}} are rejected. " + whatsAppMentionsDoc + whatsAppDestructive
	sendWhatsAppMediaDescription = "Send one WhatsApp file (document of any format, image, video or audio) to one contact or group via UAZAPI. " + whatsAppApproval + whatsAppRecipient +
		"url: public http(s) link that UAZAPI downloads; local paths are not supported. doc_name: file name shown for documents. caption: optional text, same rules as send_whatsapp (not for audio). " +
		whatsAppMentionsDoc + whatsAppDestructive
	sendWhatsAppPixDescription = "Send WhatsApp's native PIX button (recipient sees the receiver name and key, and can pay or copy it) to one contact or group via UAZAPI. " + whatsAppApproval + whatsAppRecipient +
		"pix_type: CPF, CNPJ, PHONE, EMAIL or EVP (random key). pix_key is validated (CPF/CNPJ check digits, +55 phone, email, UUID) and normalized; show the user the normalized key returned. pix_name: receiver name shown. " +
		whatsAppDestructive
)

func (s *server) addWhatsAppTools(mcpServer *mcp.Server) {
	if s.whatsapp == nil {
		return
	}
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("send_whatsapp", sendWhatsAppDescription), s.sendWhatsApp)
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("send_whatsapp_media", sendWhatsAppMediaDescription), s.sendWhatsAppMedia)
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("send_whatsapp_pix", sendWhatsAppPixDescription), s.sendWhatsAppPix)
}

type sendWhatsAppInput struct {
	Recipient string   `json:"recipient" jsonschema:"Phone number in international format (e.g. 5511999999999), a <digits>@s.whatsapp.net JID, or a group JID ending in @g.us. @lid and channels are rejected."`
	Text      string   `json:"text" jsonschema:"Message text, 1-4096 characters, sent exactly as given. Text containing {{ is rejected."`
	Mentions  []string `json:"mentions,omitempty" jsonschema:"Groups only: phone numbers to tag, or [\"all\"] to tag every member."`
}

type sendWhatsAppMediaInput struct {
	Recipient string   `json:"recipient" jsonschema:"Phone number in international format, a <digits>@s.whatsapp.net JID, or a group JID ending in @g.us."`
	Type      string   `json:"type" jsonschema:"document (any file format), image, video or audio."`
	URL       string   `json:"url" jsonschema:"Public http(s) link to the file; UAZAPI downloads it."`
	DocName   string   `json:"doc_name,omitempty" jsonschema:"File name shown to the recipient; documents only."`
	Caption   string   `json:"caption,omitempty" jsonschema:"Optional caption, up to 4096 characters, no {{. Not for audio."`
	Mentions  []string `json:"mentions,omitempty" jsonschema:"Groups only: phone numbers to tag, or [\"all\"] to tag every member."`
}

type sendWhatsAppPixInput struct {
	Recipient string `json:"recipient" jsonschema:"Phone number in international format, a <digits>@s.whatsapp.net JID, or a group JID ending in @g.us."`
	PixType   string `json:"pix_type" jsonschema:"CPF, CNPJ, PHONE, EMAIL or EVP (random key)."`
	PixKey    string `json:"pix_key" jsonschema:"The PIX key; punctuation in CPF, CNPJ and phone is accepted."`
	PixName   string `json:"pix_name,omitempty" jsonschema:"Receiver name shown on the button; UAZAPI defaults to Pix."`
}

type sendWhatsAppOutput struct {
	Sent         bool   `json:"sent"`
	RecipientJID string `json:"recipient_jid"`
	MessageID    string `json:"message_id,omitempty"`
	Status       string `json:"status,omitempty"`
	PixKey       string `json:"pix_key,omitempty"`
}

func (s *server) sendWhatsApp(ctx context.Context, _ *mcp.CallToolRequest, input sendWhatsAppInput) (*mcp.CallToolResult, sendWhatsAppOutput, error) {
	s.logAccess(ctx, "send_whatsapp")
	result, err := s.whatsapp.Send(ctx, input.Recipient, input.Text, input.Mentions)
	output, err := s.finishWhatsAppSend(ctx, "send_whatsapp", result, err, map[string]string{
		"text": input.Text, "mentions": strings.Join(input.Mentions, ","),
	})
	return nil, output, err
}

func (s *server) sendWhatsAppMedia(ctx context.Context, _ *mcp.CallToolRequest, input sendWhatsAppMediaInput) (*mcp.CallToolResult, sendWhatsAppOutput, error) {
	s.logAccess(ctx, "send_whatsapp_media")
	result, err := s.whatsapp.SendMedia(ctx, input.Recipient, whatsappsend.MediaParams{
		Type: input.Type, URL: input.URL, DocName: input.DocName, Caption: input.Caption, Mentions: input.Mentions,
	})
	output, err := s.finishWhatsAppSend(ctx, "send_whatsapp_media", result, err, map[string]string{
		"type": input.Type, "url": input.URL, "doc_name": input.DocName, "text": input.Caption, "mentions": strings.Join(input.Mentions, ","),
	})
	return nil, output, err
}

func (s *server) sendWhatsAppPix(ctx context.Context, _ *mcp.CallToolRequest, input sendWhatsAppPixInput) (*mcp.CallToolResult, sendWhatsAppOutput, error) {
	s.logAccess(ctx, "send_whatsapp_pix")
	result, err := s.whatsapp.SendPix(ctx, input.Recipient, whatsappsend.PixParams{Type: input.PixType, Key: input.PixKey, Name: input.PixName})
	output, err := s.finishWhatsAppSend(ctx, "send_whatsapp_pix", result, err, map[string]string{
		"pix_type": input.PixType, "pix_key": input.PixKey, "pix_name": input.PixName, "normalized_pix_key": result.PixKey,
	})
	return nil, output, err
}

// finishWhatsAppSend records the audit trail and maps errors to what the MCP caller may see.
func (s *server) finishWhatsAppSend(ctx context.Context, operation string, result whatsappsend.Result, err error, metadata map[string]string) (sendWhatsAppOutput, error) {
	rejected := whatsappsend.IsRejection(err)
	s.recordWhatsAppSend(ctx, operation, result, metadata, err, rejected)
	if rejected {
		return sendWhatsAppOutput{}, err
	}
	if err != nil {
		s.logger.Error("mcp tool failed", "tool", operation, "error", err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return sendWhatsAppOutput{}, err
		}
		return sendWhatsAppOutput{}, errors.New("whatsapp send failed")
	}
	return sendWhatsAppOutput{Sent: true, RecipientJID: result.RecipientJID, MessageID: result.MessageID, Status: result.Status, PixKey: result.PixKey}, nil
}

// recordWhatsAppSend keeps an audit trail of exactly what was sent or attempted.
func (s *server) recordWhatsAppSend(ctx context.Context, operation string, result whatsappsend.Result, metadata map[string]string, err error, rejected bool) {
	record := activity.Record{
		Category: "mcp", Level: "info", Operation: operation, Outcome: "sent",
		EntityType: "whatsapp_message", EntityID: result.MessageID,
	}
	metadata["recipient_jid"], metadata["message_id"], metadata["status"] = result.RecipientJID, result.MessageID, result.Status
	if err != nil {
		record.Level, record.Outcome = "error", "failed"
		if rejected {
			record.Level, record.Outcome = "warning", "rejected"
		}
		// Error strings come from validation or RemoteError/transport errors; the token is only sent as a header.
		metadata["error"] = err.Error()
	}
	record.Metadata, _ = json.Marshal(metadata)
	if recordErr := s.store.RecordActivity(ctx, record); recordErr != nil {
		s.logger.Error("activity recording failed", "category", "mcp", "operation", operation, "error", recordErr)
	}
}
