package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/memories"
	"github.com/leofelipet/contexta/internal/providers/whatsapp/uazapi"
	"github.com/leofelipet/contexta/internal/whatsappsend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type activityStore struct {
	fakeStore
	mu      sync.Mutex
	records []activity.Record
}

func (s *activityStore) RecordActivity(_ context.Context, record activity.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

func (s *activityStore) sendRecord(t *testing.T) (activity.Record, map[string]string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.Operation == "send_whatsapp" && record.EntityType == "whatsapp_message" {
			var metadata map[string]string
			if err := json.Unmarshal(record.Metadata, &metadata); err != nil {
				t.Fatal(err)
			}
			return record, metadata
		}
	}
	t.Fatalf("no send_whatsapp audit record in %#v", s.records)
	return activity.Record{}, nil
}

type recordingWhatsAppSender struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *recordingWhatsAppSender) SendText(context.Context, string, string) (uazapi.SendTextResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return uazapi.SendTextResult{}, s.err
	}
	return uazapi.SendTextResult{MessageID: "wa-msg-1", Status: "Pending"}, nil
}

func connectWhatsApp(t *testing.T, store *activityStore, sender *recordingWhatsAppSender) *mcp.ClientSession {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpServer := httptest.NewServer(New(store, memories.NewService(fakeMemoryStore{}, nil), nil, nil, whatsappsend.NewService(sender), "test-token", logger))
	t.Cleanup(httpServer.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "contexta-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "test-token"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callSendWhatsApp(t *testing.T, session *mcp.ClientSession, recipient, text string) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "send_whatsapp", Arguments: map[string]any{"recipient": recipient, "text": text},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMCPSendWhatsAppRegisteredAsDestructive(t *testing.T) {
	t.Parallel()
	session := connectWhatsApp(t, &activityStore{}, &recordingWhatsAppSender{})
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "send_whatsapp" {
			if tool.Annotations == nil || !*tool.Annotations.DestructiveHint || !*tool.Annotations.OpenWorldHint || tool.Description != sendWhatsAppDescription {
				t.Fatalf("send_whatsapp tool = %#v", tool)
			}
			return
		}
	}
	t.Fatal("send_whatsapp is not registered")
}

func TestMCPSendWhatsAppRejectsWithoutSending(t *testing.T) {
	t.Parallel()
	store, sender := &activityStore{}, &recordingWhatsAppSender{}
	session := connectWhatsApp(t, store, sender)
	if result := callSendWhatsApp(t, session, "120363000000000000@g.us", "hi"); !result.IsError {
		t.Fatal("group recipient should be rejected")
	}
	if sender.calls != 0 {
		t.Fatalf("sender calls = %d", sender.calls)
	}
	record, metadata := store.sendRecord(t)
	if record.Outcome != "rejected" || metadata["text"] != "hi" || metadata["error"] == "" {
		t.Fatalf("record = %#v, metadata = %#v", record, metadata)
	}
}

func TestMCPSendWhatsAppRecordsSentMessage(t *testing.T) {
	t.Parallel()
	store, sender := &activityStore{}, &recordingWhatsAppSender{}
	session := connectWhatsApp(t, store, sender)
	result := callSendWhatsApp(t, session, "+55 11 99999-9999", "Olá, Maria!")
	if result.IsError || sender.calls != 1 {
		t.Fatalf("result = %#v, calls = %d", result, sender.calls)
	}
	var output sendWhatsAppOutput
	data, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(data, &output); err != nil || output != (sendWhatsAppOutput{Sent: true, RecipientJID: "5511999999999@s.whatsapp.net", MessageID: "wa-msg-1", Status: "Pending"}) {
		t.Fatalf("output = %#v, error = %v", output, err)
	}
	record, metadata := store.sendRecord(t)
	if record.Outcome != "sent" || record.Level != "info" || record.EntityID != "wa-msg-1" ||
		metadata["recipient_jid"] != "5511999999999@s.whatsapp.net" || metadata["text"] != "Olá, Maria!" || metadata["message_id"] != "wa-msg-1" {
		t.Fatalf("record = %#v, metadata = %#v", record, metadata)
	}
}

func TestMCPSendWhatsAppRecordsFailure(t *testing.T) {
	t.Parallel()
	store, sender := &activityStore{}, &recordingWhatsAppSender{err: errors.New("UAZAPI returned status 500")}
	session := connectWhatsApp(t, store, sender)
	if result := callSendWhatsApp(t, session, "5511999999999", "hi"); !result.IsError || sender.calls != 1 {
		t.Fatalf("result = %#v, calls = %d", result, sender.calls)
	}
	record, metadata := store.sendRecord(t)
	if record.Outcome != "failed" || record.Level != "error" || metadata["error"] != "UAZAPI returned status 500" || metadata["recipient_jid"] != "5511999999999@s.whatsapp.net" {
		t.Fatalf("record = %#v, metadata = %#v", record, metadata)
	}
}
