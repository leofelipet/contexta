package uazapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/leofelipet/contexta/internal/ingestion"
)

var ErrUnsupportedEvent = errors.New("unsupported UAZAPI event")

type DecodedEvent struct {
	Type     string
	Messages []ingestion.Message
}

func DecodeEvent(data []byte) (DecodedEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return DecodedEvent{}, fmt.Errorf("decode UAZAPI payload: %w", err)
	}

	eventType := normalizeEventType(firstString(payload, "EventType", "event", "eventType", "type"))
	if eventType == "" {
		return DecodedEvent{}, errors.New("UAZAPI payload is missing event type")
	}
	if eventType == "connection" {
		return DecodedEvent{Type: eventType}, nil
	}
	if eventType != "messages" && eventType != "messages_update" && eventType != "history" {
		return DecodedEvent{}, fmt.Errorf("%w: %s", ErrUnsupportedEvent, eventType)
	}

	candidates := collectMessageCandidates(payload)
	messages := make([]ingestion.Message, 0, len(candidates))
	for _, candidate := range candidates {
		message, err := normalizeMessage(candidate.message, candidate.chat, eventType == "messages_update")
		if err != nil {
			return DecodedEvent{}, err
		}
		messages = append(messages, message)
	}
	if len(messages) == 0 {
		return DecodedEvent{}, fmt.Errorf("UAZAPI %s payload has no messages", eventType)
	}
	return DecodedEvent{Type: eventType, Messages: messages}, nil
}

func normalizeMessage(raw, chat map[string]any, updateEvent bool) (ingestion.Message, error) {
	providerMessageID := firstString(raw, "messageid", "messageId", "message_id")
	if providerMessageID == "" {
		return ingestion.Message{}, errors.New("UAZAPI message has no messageid")
	}
	chatID := firstString(raw, "chatid", "chatId", "chat_id")
	timestamp := parseTimestamp(firstValue(raw, "messageTimestamp", "timestamp", "message_timestamp"))
	messageType := normalizeMessageType(firstString(raw, "messageType", "type", "message_type"))
	text := firstString(raw, "text", "body", "caption")
	status := firstString(raw, "status")

	metadata, err := selectedMetadata(raw)
	if err != nil {
		return ingestion.Message{}, err
	}
	if updateEvent {
		return ingestion.Message{
			UpdateOnly:        true,
			ProviderMessageID: providerMessageID,
			Type:              messageType,
			Text:              text,
			Status:            status,
			Metadata:          metadata,
		}, nil
	}
	if chatID == "" || timestamp.IsZero() {
		return ingestion.Message{}, fmt.Errorf("UAZAPI message %q is missing chatid or timestamp", providerMessageID)
	}

	fromMe := flexibleBool(firstValue(raw, "fromMe", "from_me"))
	direction := "inbound"
	if fromMe {
		direction = "outbound"
	}

	sender := contactFromMessage(raw)
	conversationType := conversationType(chatID, flexibleBool(firstValue(raw, "isGroup", "is_group")))
	conversation := ingestion.Conversation{
		ProviderID: chatID,
		Type:       conversationType,
		Title:      firstString(raw, "groupName", "chatName", "conversationName"),
	}
	if conversation.Title == "" && conversationType == "group" {
		conversation.Title = firstString(chat, "name", "wa_name")
	}
	if conversationType == "direct" {
		counterparty := contactFromChat(chat, chatID)
		if counterparty.ProviderID == "" {
			counterparty = contactFromJID(chatID, "")
		}
		if !fromMe && sender != nil && (counterparty.Name == "" || counterparty.Phone == "") {
			if counterparty.Name == "" {
				counterparty.Name = sender.Name
				counterparty.PushName = sender.PushName
			}
			if counterparty.Phone == "" {
				counterparty.Phone = sender.Phone
			}
			counterparty.Identities = append(counterparty.Identities, sender.Identities...)
		}
		if !fromMe && sender != nil && counterparty.ProviderID == "" {
			counterparty = *sender
		}
		conversation.Contact = &counterparty
		if conversation.Title == "" {
			conversation.Title = firstDisplayValue(counterparty.Name, counterparty.PushName, counterparty.Phone)
		}
	}

	var audio *ingestion.Audio
	if messageType == "audio" {
		content, _ := raw["content"].(map[string]any)
		audio = &ingestion.Audio{
			MIMEType: firstString(content, "mimetype", "mimeType"),
			Size:     flexibleInt64(firstValue(content, "fileLength", "file_length", "size")),
			Duration: time.Duration(flexibleInt64(firstValue(content, "seconds", "duration"))) * time.Second,
			PTT:      flexibleBool(firstValue(content, "PTT", "ptt")),
		}
	}

	return ingestion.Message{
		ProviderMessageID:        providerMessageID,
		ProviderRecordID:         firstString(raw, "id"),
		Conversation:             conversation,
		Sender:                   sender,
		Direction:                direction,
		Type:                     messageType,
		Text:                     text,
		Status:                   status,
		OccurredAt:               timestamp,
		ReplyToProviderMessageID: firstString(raw, "quoted", "replyid", "replyId"),
		Metadata:                 metadata,
		Audio:                    audio,
	}, nil
}

type messageCandidate struct {
	message map[string]any
	chat    map[string]any
}

func collectMessageCandidates(payload map[string]any) []messageCandidate {
	var result []messageCandidate
	var visit func(any, map[string]any)
	visit = func(value any, chat map[string]any) {
		switch current := value.(type) {
		case map[string]any:
			if currentChat, ok := current["chat"].(map[string]any); ok {
				chat = currentChat
			}
			if firstString(current, "messageid", "messageId", "message_id") != "" {
				result = append(result, messageCandidate{message: current, chat: chat})
				return
			}
			for _, key := range []string{"data", "message", "messages", "items", "history"} {
				if child, ok := current[key]; ok {
					visit(child, chat)
				}
			}
		case []any:
			for _, child := range current {
				visit(child, chat)
			}
		}
	}
	visit(payload, nil)
	return result
}

func contactFromMessage(raw map[string]any) *ingestion.Contact {
	pn := firstString(raw, "sender_pn", "senderPn")
	lid := firstString(raw, "sender_lid", "senderLid")
	sender := firstString(raw, "sender", "from")
	providerID := firstNonEmpty(pn, lid, sender)
	if providerID == "" {
		return nil
	}
	contact := contactFromJID(providerID, firstString(raw, "senderName", "pushName"))
	for kind, value := range map[string]string{"pn": pn, "lid": lid, "jid": sender} {
		if value != "" {
			contact.Identities = append(contact.Identities, ingestion.Identity{Kind: kind, Value: value})
		}
	}
	return &contact
}

func contactFromJID(jid, name string) ingestion.Contact {
	phone := ""
	if strings.HasSuffix(jid, "@s.whatsapp.net") {
		phone = strings.TrimSuffix(jid, "@s.whatsapp.net")
	}
	return ingestion.Contact{
		ProviderID: jid,
		Identities: []ingestion.Identity{{Kind: "jid", Value: jid}},
		Phone:      phone,
		Name:       name,
		PushName:   name,
	}
}

func contactFromChat(chat map[string]any, fallbackID string) ingestion.Contact {
	jid := firstString(chat, "wa_chatid")
	lid := firstString(chat, "wa_chatlid")
	providerID := firstNonEmpty(jid, lid, fallbackID)
	phone := firstString(chat, "phone")
	if phone == "" && strings.HasSuffix(jid, "@s.whatsapp.net") {
		phone = strings.TrimSuffix(jid, "@s.whatsapp.net")
	}
	name := firstString(chat, "wa_contactName", "lead_fullName", "lead_name", "name")
	pushName := firstString(chat, "wa_name")
	identities := make([]ingestion.Identity, 0, 2)
	if jid != "" {
		identities = append(identities, ingestion.Identity{Kind: "jid", Value: jid})
	}
	if lid != "" {
		identities = append(identities, ingestion.Identity{Kind: "lid", Value: lid})
	}
	return ingestion.Contact{ProviderID: providerID, Identities: identities, Phone: phone, Name: name, PushName: pushName}
}

func selectedMetadata(raw map[string]any) (json.RawMessage, error) {
	metadata := make(map[string]any)
	for _, key := range []string{
		"reaction", "edited", "vote", "convertOptions",
		"buttonOrListid", "wasSentByApi", "source", "sender_pn", "sender_lid",
		"sendFunction", "track_source", "track_id", "error",
	} {
		if value, ok := raw[key]; ok {
			metadata[key] = value
		}
	}
	if content, ok := raw["content"].(map[string]any); ok {
		safeContent := make(map[string]any)
		for _, key := range []string{"mimetype", "fileLength", "seconds", "PTT"} {
			if value, ok := content[key]; ok {
				safeContent[key] = value
			}
		}
		if len(safeContent) > 0 {
			metadata["content"] = safeContent
		}
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode UAZAPI metadata: %w", err)
	}
	return data, nil
}

func normalizeEventType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "message", "message.received", "messages":
		return "messages"
	case "message_update", "message.status", "messages_update", "status":
		return "messages_update"
	case "history", "history_sync":
		return "history"
	case "connection", "instance.status":
		return "connection"
	default:
		return value
	}
}

func normalizeMessageType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "audio", "audiomessage", "ptt", "voice", "voice_message":
		return "audio"
	default:
		return value
	}
}

func firstDisplayValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !isWhatsAppID(value) {
			return value
		}
	}
	return ""
}

func isWhatsAppID(value string) bool {
	for _, suffix := range []string{"@lid", "@s.whatsapp.net", "@g.us", "@newsletter"} {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func conversationType(chatID string, isGroup bool) string {
	switch {
	case isGroup || strings.HasSuffix(chatID, "@g.us"):
		return "group"
	case strings.HasSuffix(chatID, "@newsletter"):
		return "newsletter"
	case strings.HasSuffix(chatID, "@s.whatsapp.net"), strings.HasSuffix(chatID, "@lid"):
		return "direct"
	default:
		return "unknown"
	}
}

func parseTimestamp(value any) time.Time {
	var number int64
	switch current := value.(type) {
	case json.Number:
		number, _ = current.Int64()
	case float64:
		number = int64(current)
	case int64:
		number = current
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, current); err == nil {
			return parsed.UTC()
		}
		number, _ = strconv.ParseInt(current, 10, 64)
	}
	if number <= 0 {
		return time.Time{}
	}
	if number < 100_000_000_000 {
		return time.Unix(number, 0).UTC()
	}
	return time.UnixMilli(number).UTC()
}

func flexibleBool(value any) bool {
	switch current := value.(type) {
	case bool:
		return current
	case json.Number:
		return current.String() == "1"
	case float64:
		return current == 1
	case string:
		parsed, _ := strconv.ParseBool(current)
		return parsed || current == "1"
	default:
		return false
	}
}

func flexibleInt64(value any) int64 {
	switch current := value.(type) {
	case json.Number:
		number, _ := current.Int64()
		return number
	case float64:
		return int64(current)
	case int64:
		return current
	case string:
		number, _ := strconv.ParseInt(current, 10, 64)
		return number
	default:
		return 0
	}
}

func firstString(values map[string]any, keys ...string) string {
	value := firstValue(values, keys...)
	switch current := value.(type) {
	case string:
		return strings.TrimSpace(current)
	case json.Number:
		return current.String()
	default:
		return ""
	}
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil {
			return value
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
