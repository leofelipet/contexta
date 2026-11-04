package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	return s.operationRecord(t, "send_whatsapp")
}

func (s *activityStore) operationRecord(t *testing.T, operation string) (activity.Record, map[string]string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.Operation == operation && record.EntityType == "whatsapp_message" {
			var metadata map[string]string
			if err := json.Unmarshal(record.Metadata, &metadata); err != nil {
				t.Fatal(err)
			}
			return record, metadata
		}
	}
	t.Fatalf("no %s audit record in %#v", operation, s.records)
	return activity.Record{}, nil
}

type recordingWhatsAppSender struct {
	mu       sync.Mutex
	calls    int
	mentions string
	media    uazapi.Media
	pix      uazapi.Pix
	err      error
}

func (s *recordingWhatsAppSender) record() (uazapi.SendTextResult, error) {
	s.calls++
	if s.err != nil {
		return uazapi.SendTextResult{}, s.err
	}
	return uazapi.SendTextResult{MessageID: "wa-msg-1", Status: "Pending"}, nil
}

func (s *recordingWhatsAppSender) SendText(_ context.Context, _, _, mentions string) (uazapi.SendTextResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mentions = mentions
	return s.record()
}

func (s *recordingWhatsAppSender) SendMedia(_ context.Context, media uazapi.Media) (uazapi.SendTextResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media = media
	return s.record()
}

func (s *recordingWhatsAppSender) SendPix(_ context.Context, pix uazapi.Pix) (uazapi.SendTextResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pix = pix
	return s.record()
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
	return callWhatsAppTool(t, session, "send_whatsapp", map[string]any{"recipient": recipient, "text": text})
}

func callWhatsAppTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
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
	want := map[string]string{
		"send_whatsapp": sendWhatsAppDescription, "send_whatsapp_media": sendWhatsAppMediaDescription, "send_whatsapp_pix": sendWhatsAppPixDescription,
	}
	for _, tool := range tools.Tools {
		if description, ok := want[tool.Name]; ok {
			if tool.Annotations == nil || !*tool.Annotations.DestructiveHint || !*tool.Annotations.OpenWorldHint || tool.Description != description {
				t.Fatalf("%s tool = %#v", tool.Name, tool)
			}
			delete(want, tool.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("not registered: %v", want)
	}
}

func TestMCPSendWhatsAppRejectsWithoutSending(t *testing.T) {
	t.Parallel()
	store, sender := &activityStore{}, &recordingWhatsAppSender{}
	session := connectWhatsApp(t, store, sender)
	if result := callSendWhatsApp(t, session, "123456789@lid", "hi"); !result.IsError {
		t.Fatal("@lid recipient should be rejected")
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
	result := callSendWhatsApp(t, session, "5511999999999", "hi")
	if !result.IsError || sender.calls != 1 || len(result.Content) == 0 {
		t.Fatalf("result = %#v, calls = %d", result, sender.calls)
	}
	if text, ok := result.Content[0].(*mcp.TextContent); !ok || text.Text != "whatsapp send failed: UAZAPI returned status 500" {
		t.Fatalf("error content = %#v", result.Content[0])
	}
	record, metadata := store.sendRecord(t)
	if record.Outcome != "failed" || record.Level != "error" || metadata["error"] != "UAZAPI returned status 500" || metadata["recipient_jid"] != "5511999999999@s.whatsapp.net" {
		t.Fatalf("record = %#v, metadata = %#v", record, metadata)
	}
}

func TestMCPSendWhatsAppToGroupWithMentions(t *testing.T) {
	t.Parallel()
	store, sender := &activityStore{}, &recordingWhatsAppSender{}
	session := connectWhatsApp(t, store, sender)
	result := callWhatsAppTool(t, session, "send_whatsapp", map[string]any{
		"recipient": "120363000000000000@g.us", "text": "Oi @5511999999999", "mentions": []string{"+55 11 99999-9999"},
	})
	if result.IsError || sender.calls != 1 || sender.mentions != "5511999999999" {
		t.Fatalf("result = %#v, sender = %#v", result, sender)
	}
	if _, metadata := store.sendRecord(t); metadata["recipient_jid"] != "120363000000000000@g.us" || metadata["mentions"] != "+55 11 99999-9999" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestMCPSendWhatsAppMedia(t *testing.T) {
	t.Parallel()
	store, sender := &activityStore{}, &recordingWhatsAppSender{}
	session := connectWhatsApp(t, store, sender)
	result := callWhatsAppTool(t, session, "send_whatsapp_media", map[string]any{
		"recipient": "5511999999999", "type": "document", "url": "https://x.test/a.pdf", "doc_name": "Contrato.pdf", "caption": "segue",
	})
	if result.IsError || sender.media.File != "https://x.test/a.pdf" || sender.media.DocName != "Contrato.pdf" {
		t.Fatalf("result = %#v, media = %#v", result, sender.media)
	}
	if record, metadata := store.operationRecord(t, "send_whatsapp_media"); record.Outcome != "sent" || metadata["url"] != "https://x.test/a.pdf" || metadata["text"] != "segue" {
		t.Fatalf("record = %#v, metadata = %#v", record, metadata)
	}
	if result := callWhatsAppTool(t, session, "send_whatsapp_media", map[string]any{
		"recipient": "5511999999999", "type": "document", "url": "/home/leo/a.pdf",
	}); !result.IsError || sender.calls != 1 {
		t.Fatalf("local path should be rejected: %#v, calls = %d", result, sender.calls)
	}
}

func TestMCPSendWhatsAppPix(t *testing.T) {
	t.Parallel()
	store, sender := &activityStore{}, &recordingWhatsAppSender{}
	session := connectWhatsApp(t, store, sender)
	result := callWhatsAppTool(t, session, "send_whatsapp_pix", map[string]any{
		"recipient": "5511999999999", "pix_type": "cpf", "pix_key": "529.982.247-25", "pix_name": "Leo",
	})
	var output sendWhatsAppOutput
	data, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(data, &output); err != nil || result.IsError || output.PixKey != "52998224725" || sender.pix.PixKey != "52998224725" {
		t.Fatalf("result = %#v, output = %#v, pix = %#v", result, output, sender.pix)
	}
	if result := callWhatsAppTool(t, session, "send_whatsapp_pix", map[string]any{
		"recipient": "5511999999999", "pix_type": "CPF", "pix_key": "529.982.247-24",
	}); !result.IsError || sender.calls != 1 {
		t.Fatalf("bad CPF should be rejected: %#v, calls = %d", result, sender.calls)
	}
	if record, metadata := store.operationRecord(t, "send_whatsapp_pix"); record.Outcome != "sent" || metadata["normalized_pix_key"] != "52998224725" {
		t.Fatalf("record = %#v, metadata = %#v", record, metadata)
	}
}

func TestSendErrorDetailHidesInstanceURL(t *testing.T) {
	t.Parallel()
	err := &url.Error{Op: "Post", URL: "https://minha-instancia.uazapi.com/send/media", Err: errors.New("connection reset by peer")}
	if got := sendErrorDetail(err); got != "UAZAPI request failed: connection reset by peer" {
		t.Fatalf("detail = %q", got)
	}
}
