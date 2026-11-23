package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leofelipet/contexta/internal/email"
	"github.com/leofelipet/contexta/internal/mcpkeys"
	"github.com/leofelipet/contexta/internal/memories"
	"github.com/leofelipet/contexta/internal/pushover"
	"github.com/leofelipet/contexta/internal/storage/postgres"
	"github.com/leofelipet/contexta/internal/whatsappsend"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testKeys accepts "test-token" with full access and "read-token" read-only.
type testKeys struct{}

func (testKeys) AuthenticateMCPKey(_ context.Context, hash string) (mcpkeys.Key, error) {
	switch hash {
	case mcpkeys.Hash("test-token"):
		return mcpkeys.Key{ID: "k1", Access: mcpkeys.AccessWrite}, nil
	case mcpkeys.Hash("read-token"):
		return mcpkeys.Key{ID: "k2", Access: mcpkeys.AccessRead}, nil
	case mcpkeys.Hash("custom-token"):
		return mcpkeys.Key{ID: "k3", Access: mcpkeys.AccessCustom, Tools: []string{"get_contact", "create_task"}}, nil
	}
	return mcpkeys.Key{}, postgres.ErrNotFound
}

func testHandler(store Store, memoriesService *memories.Service, emailService *email.Service, pushoverService *pushover.Service, whatsappService *whatsappsend.Service, logger *slog.Logger) http.Handler {
	handler, err := New(store, memoriesService, emailService, pushoverService, whatsappService, testKeys{}, logger)
	if err != nil {
		panic(err)
	}
	return handler
}

func TestMCPKeyScopes(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpServer := httptest.NewServer(testHandler(fakeStore{}, memories.NewService(fakeMemoryStore{}, nil), nil, nil, nil, logger))
	defer httpServer.Close()

	request, _ := http.NewRequest(http.MethodPost, httpServer.URL, strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer unknown")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown key status = %d", response.StatusCode)
	}

	connect := func(token string) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "contexta-test", Version: "1.0.0"}, nil)
		session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
			Endpoint: httpServer.URL, HTTPClient: &http.Client{Transport: bearerTransport{token: token}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}

	read := connect("read-token")
	tools, err := read.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("read key sees no tools")
	}
	for _, tool := range tools.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Fatalf("read key sees write tool %s", tool.Name)
		}
	}
	if _, err := read.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_task", Arguments: map[string]any{"title": "x"}}); err == nil {
		t.Fatal("read key called create_task")
	}
	if _, err := read.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_contact", Arguments: map[string]any{"id": "contact-1"}}); err != nil {
		t.Fatalf("read key get_contact: %v", err)
	}

	custom := connect("custom-token")
	tools, err = custom.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("custom key tools = %d, want 2", len(tools.Tools))
	}
	if _, err := custom.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_contacts", Arguments: map[string]any{}}); err == nil {
		t.Fatal("custom key called list_contacts")
	}
}
