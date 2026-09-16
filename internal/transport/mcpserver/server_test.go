package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/contacts"
	"github.com/leofelipet/contexta/internal/conversations"
	"github.com/leofelipet/contexta/internal/messages"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeStore struct{}

func (fakeStore) RecordActivity(context.Context, activity.Record) error { return nil }

func (fakeStore) ListContacts(context.Context, contacts.ListParams) (contacts.Page, error) {
	return contacts.Page{Contacts: []contacts.Contact{{ID: "contact-1", Name: "Alice"}}}, nil
}
func (fakeStore) GetContact(context.Context, string) (contacts.Contact, error) {
	return contacts.Contact{ID: "contact-1", Name: "Alice"}, nil
}
func (fakeStore) ListConversations(context.Context, conversations.ListParams) (conversations.Page, error) {
	return conversations.Page{}, nil
}
func (fakeStore) GetConversation(context.Context, string) (conversations.Conversation, error) {
	return conversations.Conversation{ID: "conversation-1"}, nil
}
func (fakeStore) SearchMessages(context.Context, messages.SearchParams) (messages.Page, error) {
	return messages.Page{}, nil
}
func (fakeStore) ListUnreadMessages(context.Context, messages.UnreadParams) (messages.Page, error) {
	return messages.Page{Messages: []messages.Message{{ID: "00000000-0000-0000-0000-000000000001"}}}, nil
}
func (fakeStore) AcknowledgeMessages(context.Context, string, []string) (int, error) {
	return 1, nil
}
func (fakeStore) GetMessagesAround(context.Context, string, int, int) (messages.Around, error) {
	return messages.Around{}, nil
}

type bearerTransport struct {
	token string
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(request)
}

func TestMCPToolsOverStreamableHTTP(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpServer := httptest.NewServer(New(fakeStore{}, "test-token", logger))
	defer httpServer.Close()

	unauthorized, err := http.Get(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.StatusCode)
	}

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
	if len(tools.Tools) != 9 {
		t.Fatalf("tools = %d, want 9", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil {
			t.Fatalf("tool %s has no annotations", tool.Name)
		}
		if tool.Name == "acknowledge_messages" {
			if tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || *tool.Annotations.DestructiveHint {
				t.Fatalf("acknowledge annotations = %#v", tool.Annotations)
			}
		} else if !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s is not marked read-only", tool.Name)
		}
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_contacts", Arguments: map[string]any{"limit": 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.StructuredContent == nil {
		t.Fatalf("result = %#v", result)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "acknowledge_messages", Arguments: map[string]any{
			"consumer_id": "primary-agent", "message_ids": []string{"00000000-0000-0000-0000-000000000001"},
		},
	})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("acknowledge result = %#v, err = %v", result, err)
	}
}
