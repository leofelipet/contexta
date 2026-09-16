package uazapi

import (
	"testing"
	"time"
)

func TestDecodeMessagesEnvelope(t *testing.T) {
	t.Parallel()
	payload := []byte(`{
		"EventType":"messages",
		"data":{"messages":[{
			"id":"provider-record",
			"messageid":"3EB0ABC",
			"chatid":"5511999999999@s.whatsapp.net",
			"sender":"5511999999999@s.whatsapp.net",
			"senderName":"Alice",
			"fromMe":false,
			"messageType":"text",
			"messageTimestamp":1789560000000,
			"text":"hello"
		}]}
	}`)
	event, err := DecodeEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "messages" || len(event.Messages) != 1 {
		t.Fatalf("event = %#v", event)
	}
	message := event.Messages[0]
	if message.Direction != "inbound" || message.Conversation.Type != "direct" {
		t.Fatalf("message = %#v", message)
	}
	if message.OccurredAt != time.UnixMilli(1789560000000).UTC() {
		t.Fatalf("timestamp = %v", message.OccurredAt)
	}
}

func TestDecodePartialUpdate(t *testing.T) {
	t.Parallel()
	event, err := DecodeEvent([]byte(`{
		"event":"messages_update",
		"data":{"messageid":"message-1","status":"Read"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(event.Messages) != 1 || !event.Messages[0].UpdateOnly || event.Messages[0].Status != "Read" {
		t.Fatalf("event = %#v", event)
	}
}
