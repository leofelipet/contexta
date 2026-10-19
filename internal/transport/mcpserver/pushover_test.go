package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	emailcrypto "github.com/leofelipet/contexta/internal/email/crypto"
	"github.com/leofelipet/contexta/internal/memories"
	provider "github.com/leofelipet/contexta/internal/providers/notifications/pushover"
	"github.com/leofelipet/contexta/internal/pushover"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type configuredPushoverStore struct {
	fakePushoverStore
	app, user []byte
}

func (s configuredPushoverStore) GetPushoverCredentials(context.Context) (pushover.Settings, []byte, []byte, error) {
	return pushover.Settings{Configured: true, Enabled: true}, s.app, s.user, nil
}

type recordingPushoverClient struct{ params provider.SendParams }

func (c *recordingPushoverClient) Send(_ context.Context, _ provider.Credentials, params provider.SendParams) (provider.Result, error) {
	c.params = params
	return provider.Result{Request: "request-id", Receipt: "receipt-id"}, nil
}

func (c *recordingPushoverClient) Sounds(context.Context, string) ([]provider.Sound, error) {
	return []provider.Sound{{ID: "siren", Name: "Siren"}}, nil
}

func TestMCPSendPushoverEmergencyReturnsReceipt(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	app, err := emailcrypto.Encrypt(key, []byte("app"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := emailcrypto.Encrypt(key, []byte("user"))
	if err != nil {
		t.Fatal(err)
	}
	pushoverClient := &recordingPushoverClient{}
	pushoverService := pushover.NewService(configuredPushoverStore{app: app, user: user}, key, pushoverClient)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpServer := httptest.NewServer(New(fakeStore{}, memories.NewService(fakeMemoryStore{}, nil), nil, pushoverService, "test-token", logger))
	defer httpServer.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "contexta-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{token: "test-token"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tool *mcp.Tool
	for _, candidate := range tools.Tools {
		if candidate.Name == "send_pushover_notification" {
			tool = candidate
		}
	}
	if tool == nil || !strings.Contains(tool.Description, "Emergency priority 2") {
		t.Fatalf("send_pushover_notification tool = %#v", tool)
	}
	schema, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"retry"`, `"expire"`, `"sound"`, "to 2 (emergency)"} {
		if !strings.Contains(string(schema), want) {
			t.Fatalf("input schema missing %s: %s", want, schema)
		}
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "send_pushover_notification", Arguments: map[string]any{"message": "hello", "priority": 2, "retry": 45, "expire": 900, "sound": "siren"},
	})
	if err != nil || result.IsError {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	output, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"receipt":"receipt-id","request":"request-id","sent":true}` {
		t.Fatalf("output = %s", output)
	}
	if pushoverClient.params.Priority != 2 || pushoverClient.params.Retry != 45 || pushoverClient.params.Expire != 900 || pushoverClient.params.Sound != "siren" {
		t.Fatalf("params = %+v", pushoverClient.params)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "send_pushover_notification", Arguments: map[string]any{"message": "hello", "priority": 1, "retry": 60},
	})
	if err != nil || !result.IsError {
		t.Fatalf("retry without emergency result = %#v, err = %v", result, err)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_pushover_sounds"})
	if err != nil || result.IsError {
		t.Fatalf("list_pushover_sounds result = %#v, err = %v", result, err)
	}
	output, err = json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"sounds":[{"id":"siren","name":"Siren"}]}` {
		t.Fatalf("list_pushover_sounds output = %s", output)
	}
}
