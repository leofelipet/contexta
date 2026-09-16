package uazapi

import (
	"strings"
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

func TestDecodeAudioMessage(t *testing.T) {
	t.Parallel()
	payload := []byte(`{
		"EventType":"messages",
		"chat":{"name":"Grupo Família","wa_name":"Grupo Família","wa_chatid":"120363@g.us","wa_isGroup":true},
		"message":{
			"id":"owner:ABC123","messageid":"ABC123","chatid":"120363@g.us",
			"groupName":"Grupo Família","senderName":"Leonardo",
			"sender_lid":"123@lid","sender_pn":"5514999999999@s.whatsapp.net",
			"fromMe":true,"isGroup":true,"messageType":"AudioMessage","mediaType":"ptt",
			"messageTimestamp":1789571165000,
			"content":{"URL":"https://media.example/audio.enc","mimetype":"audio/ogg; codecs=opus","fileLength":7065,"seconds":2,"PTT":true,"mediaKey":"secret"}
		}
	}`)
	event, err := DecodeEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	message := event.Messages[0]
	if message.Type != "audio" || message.Audio == nil {
		t.Fatalf("audio = %#v", message)
	}
	if message.Audio.MIMEType != "audio/ogg; codecs=opus" || message.Audio.Size != 7065 || message.Audio.Duration != 2*time.Second || !message.Audio.PTT {
		t.Fatalf("audio metadata = %#v", message.Audio)
	}
	if message.Conversation.Title != "Grupo Família" {
		t.Fatalf("conversation title = %q", message.Conversation.Title)
	}
	metadata := string(message.Metadata)
	if strings.Contains(metadata, "mediaKey") || strings.Contains(metadata, "audio.enc") {
		t.Fatalf("sensitive media data persisted: %s", metadata)
	}
}

func TestDecodeDirectConversationUsesPushNameInsteadOfLID(t *testing.T) {
	t.Parallel()
	payload := []byte(`{
		"EventType":"messages",
		"chat":{"wa_chatid":"5514999999999@s.whatsapp.net","wa_chatlid":"123456@lid","wa_name":"Talita","phone":"5514999999999"},
		"message":{"messageid":"ABC124","chatid":"123456@lid","fromMe":true,"messageType":"text","messageTimestamp":1789571165000,"text":"oi"}
	}`)
	event, err := DecodeEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	conversation := event.Messages[0].Conversation
	if conversation.Title != "Talita" || conversation.Contact == nil || conversation.Contact.Phone != "5514999999999" {
		t.Fatalf("conversation = %#v", conversation)
	}
	if strings.Contains(conversation.Title, "@lid") {
		t.Fatalf("LID exposed as title: %q", conversation.Title)
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
