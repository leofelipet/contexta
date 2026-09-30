package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/companies"
	"github.com/leofelipet/contexta/internal/contacts"
	"github.com/leofelipet/contexta/internal/conversations"
	"github.com/leofelipet/contexta/internal/denylist"
	"github.com/leofelipet/contexta/internal/email"
	"github.com/leofelipet/contexta/internal/memories"
	"github.com/leofelipet/contexta/internal/messages"
	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/leofelipet/contexta/internal/tasks"
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
func (fakeStore) ListDenylist(context.Context, denylist.ListParams) (denylist.Page, error) {
	return denylist.Page{}, nil
}
func (fakeStore) AddDenylistEntry(context.Context, denylist.AddParams) (denylist.Entry, error) {
	return denylist.Entry{ID: "denylist-1", TargetType: denylist.TargetConversation}, nil
}
func (fakeStore) RemoveDenylistEntry(context.Context, string) error { return nil }
func (fakeStore) ListCompanies(context.Context, companies.ListParams) (companies.Page, error) {
	return companies.Page{Companies: []companies.Company{}}, nil
}
func (fakeStore) GetCompany(_ context.Context, id string) (companies.Company, error) {
	return companies.Company{ID: id, Name: "ACME"}, nil
}
func (fakeStore) CreateCompany(_ context.Context, params companies.CreateParams) (companies.Company, error) {
	return companies.Company{ID: "1", Name: params.Name, Notes: params.Notes}, nil
}
func (fakeStore) UpdateCompany(_ context.Context, id string, _ companies.UpdateParams) (companies.Company, error) {
	return companies.Company{ID: id, Name: "ACME"}, nil
}
func (fakeStore) DeleteCompany(context.Context, string) error { return nil }
func (fakeStore) AttachContactToCompany(_ context.Context, companyID, _ string) (companies.Company, error) {
	return companies.Company{ID: companyID, Name: "ACME", ContactCount: 1}, nil
}
func (fakeStore) DetachContactFromCompany(_ context.Context, companyID, _ string) (companies.Company, error) {
	return companies.Company{ID: companyID, Name: "ACME"}, nil
}
func (fakeStore) ListTasks(context.Context, tasks.ListParams) (tasks.Page, error) {
	return tasks.Page{}, nil
}
func (fakeStore) GetTask(context.Context, string) (tasks.Task, error) {
	return tasks.Task{ID: "1", Title: "Sample"}, nil
}
func (fakeStore) CreateTask(context.Context, tasks.CreateParams) (tasks.Task, error) {
	return tasks.Task{ID: "1", Title: "Sample"}, nil
}
func (fakeStore) UpdateTask(context.Context, string, tasks.UpdateParams) (tasks.Task, tasks.MemoryCleanup, error) {
	return tasks.Task{ID: "1", Title: "Updated"}, tasks.MemoryCleanup{}, nil
}
func (fakeStore) DeleteTask(context.Context, string, bool) (tasks.DeleteResult, error) {
	return tasks.DeleteResult{Deleted: true}, nil
}
func (fakeStore) AttachTaskMemory(_ context.Context, taskID, _ string) (tasks.Task, error) {
	return tasks.Task{ID: taskID, Title: "Sample", Memories: []tasks.MemoryRef{{ID: "00000000-0000-0000-0000-000000000020", Title: "Note"}}}, nil
}
func (fakeStore) DetachTaskMemory(_ context.Context, taskID, _ string) (tasks.Task, error) {
	return tasks.Task{ID: taskID, Title: "Sample"}, nil
}
func (fakeStore) ListTaskSchedules(context.Context, schedules.ListParams) (schedules.Page, error) {
	return schedules.Page{}, nil
}
func (fakeStore) GetTaskSchedule(_ context.Context, id string) (schedules.Schedule, error) {
	return schedules.Schedule{ID: id, Cron: "0 9 * * 1-5", Timezone: schedules.DefaultTimezone, Title: "Daily", Enabled: true}, nil
}
func (fakeStore) CreateTaskSchedule(_ context.Context, params schedules.CreateParams) (schedules.Schedule, error) {
	if err := schedules.Validate(params.Cron, schedules.DefaultTimezone); err != nil {
		return schedules.Schedule{}, err
	}
	return schedules.Schedule{ID: "1", Cron: params.Cron, Timezone: schedules.DefaultTimezone, Title: params.Title, Enabled: true}, nil
}
func (fakeStore) UpdateTaskSchedule(_ context.Context, id string, _ schedules.UpdateParams) (schedules.Schedule, error) {
	return schedules.Schedule{ID: id, Cron: "0 9 * * 1-5", Timezone: schedules.DefaultTimezone, Title: "Daily"}, nil
}
func (fakeStore) DeleteTaskSchedule(context.Context, string) error { return nil }

type fakeMemoryStore struct{}

func (fakeMemoryStore) ListMemories(context.Context, memories.ListParams) (memories.Page, error) {
	return memories.Page{Memories: []memories.Memory{{ID: "00000000-0000-0000-0000-000000000020", Title: "Note", Content: "hello", Source: memories.SourceNote, EmbeddingStatus: memories.EmbeddingReady}}}, nil
}
func (fakeMemoryStore) GetMemory(_ context.Context, id string) (memories.Memory, error) {
	return memories.Memory{ID: id, Title: "Note", Content: "hello", Source: memories.SourceNote, EmbeddingStatus: memories.EmbeddingReady}, nil
}
func (fakeMemoryStore) ResolveMessageMemoryContent(context.Context, string) (string, string, string, error) {
	return "message body", "", "", nil
}
func (fakeMemoryStore) CreateMemory(_ context.Context, params memories.CreateParams, _ []float32, _ string, _ error) (memories.Memory, error) {
	return memories.Memory{ID: "00000000-0000-0000-0000-000000000020", Title: params.Title, Content: params.Content, Source: memories.SourceNote, EmbeddingStatus: memories.EmbeddingFailed}, nil
}
func (fakeMemoryStore) UpdateMemory(_ context.Context, id string, _ memories.UpdateParams, _ []float32, _ string, _ error, _ bool) (memories.Memory, error) {
	return memories.Memory{ID: id, Title: "Updated", Content: "hello", Source: memories.SourceNote, EmbeddingStatus: memories.EmbeddingReady}, nil
}
func (fakeMemoryStore) DeleteMemory(context.Context, string) error { return nil }
func (fakeMemoryStore) SearchMemories(context.Context, memories.SearchParams, []float32) (memories.SearchResult, error) {
	return memories.SearchResult{}, nil
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
	memoriesService := memories.NewService(fakeMemoryStore{}, nil)
	httpServer := httptest.NewServer(New(fakeStore{}, memoriesService, nil, "test-token", logger))
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
	if len(tools.Tools) != 37 {
		t.Fatalf("tools = %d, want 37 (without email service)", len(tools.Tools))
	}
	writeTools := map[string]bool{
		"acknowledge_messages":    true,
		"add_to_denylist":         true,
		"remove_from_denylist":    true,
		"create_task":             true,
		"update_task":             true,
		"delete_task":             true,
		"attach_memory_to_task":   true,
		"detach_memory_from_task": true,
		"save_memory":             true,
		"update_memory":           true,
		"delete_memory":           true,
		"create_task_schedule":    true,
		"update_task_schedule":    true,
		"delete_task_schedule":    true,
		"create_company":              true,
		"update_company":              true,
		"delete_company":              true,
		"attach_contact_to_company":   true,
		"detach_contact_from_company": true,
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil {
			t.Fatalf("tool %s has no annotations", tool.Name)
		}
		if writeTools[tool.Name] {
			if tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || *tool.Annotations.DestructiveHint {
				t.Fatalf("%s annotations = %#v", tool.Name, tool.Annotations)
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

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "save_memory", Arguments: map[string]any{
			"title": "Preferência", "content": "Cliente prefere contato pela manhã",
		},
	})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("save_memory result = %#v, err = %v", result, err)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_memories", Arguments: map[string]any{"limit": 10},
	})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("list_memories result = %#v, err = %v", result, err)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "delete_task", Arguments: map[string]any{"id": "1", "delete_memories": true},
	})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("delete_task result = %#v, err = %v", result, err)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "update_task", Arguments: map[string]any{"id": "1", "status": "done", "delete_memories": true},
	})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("update_task result = %#v, err = %v", result, err)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_task_schedule", Arguments: map[string]any{"cron": "0 9 * * 1-5", "title": "Revisar caixa de entrada"},
	})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("create_task_schedule result = %#v, err = %v", result, err)
	}

	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_task_schedule", Arguments: map[string]any{"cron": "* * * * *", "title": "Too often"},
	})
	if err != nil || !result.IsError {
		t.Fatalf("create_task_schedule with sub-5-minute cron should fail: %#v, err = %v", result, err)
	}
}

type fakeEmailStore struct{}

func (fakeEmailStore) ListEmailAccounts(context.Context, email.ListParams) (email.Page, error) {
	return email.Page{Accounts: []email.Account{{ID: "00000000-0000-0000-0000-0000000000aa", Name: "Work", Address: "work@example.com", Enabled: true}}}, nil
}
func (fakeEmailStore) GetEmailAccount(context.Context, string) (email.Account, error) {
	return email.Account{}, email.ErrNotFound
}
func (fakeEmailStore) GetEmailAccountSecrets(context.Context, string) (email.AccountSecrets, []byte, error) {
	return email.AccountSecrets{}, nil, email.ErrNotFound
}
func (fakeEmailStore) CreateEmailAccount(context.Context, email.CreateParams, []byte) (email.Account, error) {
	return email.Account{}, email.ErrInvalidArgument
}
func (fakeEmailStore) UpdateEmailAccount(context.Context, string, email.UpdateParams, []byte) (email.Account, error) {
	return email.Account{}, email.ErrNotFound
}
func (fakeEmailStore) DeleteEmailAccount(context.Context, string) error { return email.ErrNotFound }

func TestMCPEmailToolsRegistered(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	memoriesService := memories.NewService(fakeMemoryStore{}, nil)
	key := make([]byte, 32)
	emailService := email.NewService(fakeEmailStore{}, key, nil, nil)
	httpServer := httptest.NewServer(New(fakeStore{}, memoriesService, emailService, "test-token", logger))
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
	if len(tools.Tools) != 46 {
		t.Fatalf("tools = %d, want 46 with email", len(tools.Tools))
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"list_email_accounts", "list_mailboxes", "search_emails", "get_email", "mark_email_read", "send_email", "set_email_flags", "move_email", "delete_email"} {
		tool := byName[name]
		if tool == nil || tool.Annotations == nil {
			t.Fatalf("missing email tool %s", name)
		}
		if !*tool.Annotations.OpenWorldHint && name != "list_email_accounts" {
			t.Fatalf("%s should be open-world", name)
		}
	}
	if !*byName["send_email"].Annotations.DestructiveHint || !*byName["delete_email"].Annotations.DestructiveHint {
		t.Fatal("send/delete should be destructive")
	}
	if byName["list_email_accounts"].Annotations.OpenWorldHint != nil && *byName["list_email_accounts"].Annotations.OpenWorldHint {
		t.Fatal("list_email_accounts should stay closed-world")
	}

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_email_accounts", Arguments: map[string]any{"enabled_only": true},
	})
	if err != nil || result.IsError || result.StructuredContent == nil {
		t.Fatalf("list_email_accounts result = %#v, err = %v", result, err)
	}
}
