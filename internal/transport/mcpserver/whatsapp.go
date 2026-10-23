package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/whatsappsend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const sendWhatsAppDescription = "Send ONE WhatsApp text message to ONE individual contact via UAZAPI. NUNCA chame esta ferramenta sem antes mostrar ao Leonardo, no chat, o destinatário e o texto exato, e receber dele um 'sim' explícito para essa mensagem. Envie exatamente o texto aprovado, sem nenhuma alteração; se algo mudar, peça aprovação de novo. recipient: phone number in international format (e.g. 5511999999999) or a <digits>@s.whatsapp.net JID; groups (@g.us), @lid and channels are rejected. text: non-empty, max 4096 characters, no {{placeholders}}. Destructive: delivers a real message to an external person and cannot be undone."

func (s *server) addWhatsAppTools(mcpServer *mcp.Server) {
	if s.whatsapp == nil {
		return
	}
	mcp.AddTool(mcpServer, destructiveOpenWorldTool("send_whatsapp", sendWhatsAppDescription), s.sendWhatsApp)
}

type sendWhatsAppInput struct {
	Recipient string `json:"recipient" jsonschema:"Phone number in international format (e.g. 5511999999999) or a <digits>@s.whatsapp.net JID."`
	Text      string `json:"text" jsonschema:"Exact text approved by Leonardo; sent unchanged. Max 4096 characters, no {{placeholders}}."`
}

type sendWhatsAppOutput struct {
	Sent         bool   `json:"sent"`
	RecipientJID string `json:"recipient_jid"`
	MessageID    string `json:"message_id,omitempty"`
	Status       string `json:"status,omitempty"`
}

func (s *server) sendWhatsApp(ctx context.Context, _ *mcp.CallToolRequest, input sendWhatsAppInput) (*mcp.CallToolResult, sendWhatsAppOutput, error) {
	s.logAccess(ctx, "send_whatsapp")
	result, err := s.whatsapp.Send(ctx, input.Recipient, input.Text)
	rejected := errors.Is(err, whatsappsend.ErrInvalidRecipient) || errors.Is(err, whatsappsend.ErrInvalidText)
	s.recordWhatsAppSend(ctx, result, input.Text, err, rejected)
	if rejected {
		return nil, sendWhatsAppOutput{}, err
	}
	if err != nil {
		s.logger.Error("mcp tool failed", "tool", "send_whatsapp", "error", err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, sendWhatsAppOutput{}, err
		}
		return nil, sendWhatsAppOutput{}, errors.New("whatsapp send failed")
	}
	return nil, sendWhatsAppOutput{
		Sent: true, RecipientJID: result.RecipientJID, MessageID: result.MessageID, Status: result.Status,
	}, nil
}

// recordWhatsAppSend keeps an audit trail of exactly what was sent or attempted.
func (s *server) recordWhatsAppSend(ctx context.Context, result whatsappsend.Result, text string, err error, rejected bool) {
	record := activity.Record{
		Category: "mcp", Level: "info", Operation: "send_whatsapp", Outcome: "sent",
		EntityType: "whatsapp_message", EntityID: result.MessageID,
	}
	metadata := map[string]string{
		"recipient_jid": result.RecipientJID, "text": text,
		"message_id": result.MessageID, "status": result.Status,
	}
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
		s.logger.Error("activity recording failed", "category", "mcp", "operation", "send_whatsapp", "error", recordErr)
	}
}
