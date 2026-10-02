package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/auth"
	"github.com/leofelipet/contexta/internal/companies"
	"github.com/leofelipet/contexta/internal/contacts"
	"github.com/leofelipet/contexta/internal/conversations"
	"github.com/leofelipet/contexta/internal/denylist"
	"github.com/leofelipet/contexta/internal/email"
	"github.com/leofelipet/contexta/internal/memories"
	"github.com/leofelipet/contexta/internal/messages"
	"github.com/leofelipet/contexta/internal/pagination"
	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/leofelipet/contexta/internal/storage/postgres"
	"github.com/leofelipet/contexta/internal/tasks"
	"github.com/leofelipet/contexta/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Store interface {
	ListContacts(context.Context, contacts.ListParams) (contacts.Page, error)
	GetContact(context.Context, string) (contacts.Contact, error)
	ListConversations(context.Context, conversations.ListParams) (conversations.Page, error)
	GetConversation(context.Context, string) (conversations.Conversation, error)
	SearchMessages(context.Context, messages.SearchParams) (messages.Page, error)
	ListUnreadMessages(context.Context, messages.UnreadParams) (messages.Page, error)
	AcknowledgeMessages(context.Context, string, []string) (int, error)
	GetMessagesAround(context.Context, string, int, int) (messages.Around, error)
	ListDenylist(context.Context, denylist.ListParams) (denylist.Page, error)
	AddDenylistEntry(context.Context, denylist.AddParams) (denylist.Entry, error)
	RemoveDenylistEntry(context.Context, string) error
	RemoveDenylistEntries(context.Context, []string) (int, error)
	ListCompanies(context.Context, companies.ListParams) (companies.Page, error)
	GetCompany(context.Context, string) (companies.Company, error)
	CreateCompany(context.Context, companies.CreateParams) (companies.Company, error)
	UpdateCompany(context.Context, string, companies.UpdateParams) (companies.Company, error)
	DeleteCompany(context.Context, string) error
	DeleteCompanies(context.Context, []string) (int, error)
	AttachContactToCompany(ctx context.Context, companyID, contactID string) (companies.Company, error)
	DetachContactFromCompany(ctx context.Context, companyID, contactID string) (companies.Company, error)
	ListTasks(context.Context, tasks.ListParams) (tasks.Page, error)
	GetTask(context.Context, string) (tasks.Task, error)
	CreateTask(context.Context, tasks.CreateParams) (tasks.Task, error)
	UpdateTask(context.Context, string, tasks.UpdateParams) (tasks.Task, tasks.MemoryCleanup, error)
	DeleteTask(ctx context.Context, id string, deleteMemories bool) (tasks.DeleteResult, error)
	DeleteTasks(ctx context.Context, ids []string, deleteMemories bool) (tasks.BulkResult, error)
	UpdateTasksStatus(ctx context.Context, ids []string, status string, deleteMemories bool) (tasks.BulkResult, error)
	AttachTaskMemory(ctx context.Context, taskID, memoryID string) (tasks.Task, error)
	DetachTaskMemory(ctx context.Context, taskID, memoryID string) (tasks.Task, error)
	ListTaskSchedules(context.Context, schedules.ListParams) (schedules.Page, error)
	GetTaskSchedule(context.Context, string) (schedules.Schedule, error)
	CreateTaskSchedule(context.Context, schedules.CreateParams) (schedules.Schedule, error)
	UpdateTaskSchedule(context.Context, string, schedules.UpdateParams) (schedules.Schedule, error)
	DeleteTaskSchedule(context.Context, string) error
	DeleteTaskSchedules(context.Context, []string) (int, error)
	SetTaskSchedulesEnabled(ctx context.Context, ids []string, enabled bool) (int, error)
	RecordActivity(context.Context, activity.Record) error
}

type server struct {
	store    Store
	memories *memories.Service
	email    *email.Service
	logger   *slog.Logger
}

func New(store Store, memoriesService *memories.Service, emailService *email.Service, token string, logger *slog.Logger) http.Handler {
	implementation := &server{store: store, memories: memoriesService, email: emailService, logger: logger}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "contexta", Version: version.Version}, nil)
	implementation.addTools(mcpServer)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	return auth.NewMiddleware(token).Wrap(handler)
}

func (s *server) addTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, readOnlyTool("search_messages", "Search stored WhatsApp messages using text, time, contact, conversation, direction, and agent read-state filters."), s.searchMessages)
	mcp.AddTool(mcpServer, readOnlyTool("list_unread_messages", "List messages not yet acknowledged by a specific agent consumer. This does not mark them as read."), s.listUnreadMessages)
	mcp.AddTool(mcpServer, localWriteTool("acknowledge_messages", "Mark a batch of messages as processed by a specific agent consumer without changing WhatsApp data."), s.acknowledgeMessages)
	mcp.AddTool(mcpServer, readOnlyTool("find_conversations", "Find WhatsApp conversations by title, contact, or time range."), s.findConversations)
	mcp.AddTool(mcpServer, readOnlyTool("get_conversation", "Get one conversation by its Contexta conversation ID."), s.getConversation)
	mcp.AddTool(mcpServer, readOnlyTool("get_messages", "Get a paginated page of messages from one conversation."), s.getMessages)
	mcp.AddTool(mcpServer, readOnlyTool("get_messages_around", "Get messages immediately before and after a selected message for local context."), s.getMessagesAround)
	mcp.AddTool(mcpServer, readOnlyTool("list_contacts", "List or search stored WhatsApp contacts, optionally only those linked to a company."), s.listContacts)
	mcp.AddTool(mcpServer, readOnlyTool("get_contact", "Get one contact by its Contexta contact ID."), s.getContact)
	mcp.AddTool(mcpServer, readOnlyTool("list_denylist", "List conversations and contacts blocked from message ingestion."), s.listDenylist)
	mcp.AddTool(mcpServer, localWriteTool("add_to_denylist", "Block future message ingestion for a conversation (e.g. group) or contact (direct chat only)."), s.addToDenylist)
	mcp.AddTool(mcpServer, localWriteTool("remove_from_denylist", "Remove a denylist entry so messages from that target are ingested again."), s.removeFromDenylist)
	mcp.AddTool(mcpServer, readOnlyTool("list_tasks", "List tasks with optional filters for status, company (company_id or name substring), contact, conversation, schedule, text query (prefix with # for exact numeric ID), overdue, and open_only."), s.listTasks)
	mcp.AddTool(mcpServer, readOnlyTool("get_task", "Get one task by its numeric Contexta task ID (1, 2, 3…), including linked memories."), s.getTask)
	mcp.AddTool(mcpServer, localWriteTool("create_task", "Create a task with title, optional company_id (see list_companies), due date, status, description, and optional WhatsApp contact or conversation link. Without company_id, the task inherits the linked contact's company."), s.createTask)
	mcp.AddTool(mcpServer, localWriteTool("update_task", "Update task fields. Setting status to done sets due_at to now. Pass empty strings to clear due_at, company_id, conversation_id, or contact_id. When closing a task (done or cancelled), delete_memories=true permanently deletes memories linked only to this task; use it only when that context is no longer worth keeping."), s.updateTask)
	mcp.AddTool(mcpServer, localWriteTool("delete_task", "Permanently delete a task by ID. With delete_memories=true, also permanently deletes memories linked only to this task; memories linked to other tasks are kept."), s.deleteTask)
	mcp.AddTool(mcpServer, localWriteTool("attach_memory_to_task", "Link an existing memory to a task. A task can have unlimited memories; the same memory may link to multiple tasks."), s.attachMemoryToTask)
	mcp.AddTool(mcpServer, localWriteTool("detach_memory_from_task", "Remove the link between a task and a memory without deleting either."), s.detachMemoryFromTask)
	mcp.AddTool(mcpServer, readOnlyTool("search_memories", "Semantic search over saved agent memories using embeddings. Prefer this for recall by meaning."), s.searchMemories)
	mcp.AddTool(mcpServer, readOnlyTool("list_memories", "List saved memories with optional filters for conversation, contact, source, and text query."), s.listMemories)
	mcp.AddTool(mcpServer, readOnlyTool("get_memory", "Get one memory by its Contexta memory ID."), s.getMemory)
	mcp.AddTool(mcpServer, localWriteTool("save_memory", "Save a free-text note or a WhatsApp message as a memory for later semantic retrieval. Pass message_id to copy message text automatically."), s.saveMemory)
	mcp.AddTool(mcpServer, localWriteTool("update_memory", "Update memory title, content, or links. Changing content re-embeds the memory."), s.updateMemory)
	mcp.AddTool(mcpServer, localWriteTool("delete_memory", "Permanently delete a memory by ID."), s.deleteMemory)
	s.addCompanyTools(mcpServer)
	s.addScheduleTools(mcpServer)
	s.addBulkTools(mcpServer)
	s.addEmailTools(mcpServer)
}

func localWriteTool(name, description string) *mcp.Tool {
	openWorld := false
	destructive := false
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, IdempotentHint: true, OpenWorldHint: &openWorld, DestructiveHint: &destructive,
		},
	}
}

func readOnlyTool(name, description string) *mcp.Tool {
	openWorld := false
	destructive := false
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &openWorld, DestructiveHint: &destructive,
		},
	}
}

type searchMessagesInput struct {
	Query          string `json:"query,omitempty" jsonschema:"Text to search for in message bodies."`
	From           string `json:"from,omitempty" jsonschema:"Inclusive RFC3339 timestamp or YYYY-MM-DD date."`
	To             string `json:"to,omitempty" jsonschema:"Exclusive RFC3339 timestamp or inclusive YYYY-MM-DD date."`
	ContactID      string `json:"contact_id,omitempty" jsonschema:"Contexta contact ID."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Contexta conversation ID."`
	Direction      string `json:"direction,omitempty" jsonschema:"Message direction: inbound or outbound."`
	Type           string `json:"type,omitempty" jsonschema:"Message type such as text, image, audio, or document."`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of messages, up to 100."`
	Cursor         string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
	ConsumerID     string `json:"consumer_id,omitempty" jsonschema:"Stable agent consumer ID, required when filtering by read state."`
	ReadState      string `json:"read_state,omitempty" jsonschema:"Agent read state: all, read, or unread. Defaults to all."`
}

type messagesOutput struct {
	Messages   []messages.Message `json:"messages"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func (s *server) searchMessages(ctx context.Context, _ *mcp.CallToolRequest, input searchMessagesInput) (*mcp.CallToolResult, messagesOutput, error) {
	s.logAccess(ctx, "search_messages")
	from, to, err := parseRange(input.From, input.To)
	if err != nil {
		return nil, messagesOutput{}, err
	}
	page, err := s.store.SearchMessages(ctx, messages.SearchParams{
		Query: input.Query, From: from, To: to, ContactID: input.ContactID,
		ConversationID: input.ConversationID, Direction: input.Direction,
		Type: input.Type, Limit: mcpLimit(input.Limit), Cursor: input.Cursor,
		ConsumerID: input.ConsumerID, ReadState: input.ReadState,
	})
	if err != nil {
		s.logError(ctx, "search_messages", err)
		return nil, messagesOutput{}, safeToolError(err)
	}
	return nil, messagesOutput{Messages: page.Messages, NextCursor: page.NextCursor}, nil
}

type unreadMessagesInput struct {
	ConsumerID     string `json:"consumer_id" jsonschema:"Required stable agent consumer ID using letters, numbers, dot, underscore, colon, or hyphen."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Optional Contexta conversation ID."`
	Direction      string `json:"direction,omitempty" jsonschema:"Message direction: inbound or outbound."`
	Type           string `json:"type,omitempty" jsonschema:"Message type such as text, image, audio, or document."`
	Order          string `json:"order,omitempty" jsonschema:"Sort order: newest or oldest. Defaults to newest."`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of messages, up to 100."`
	Cursor         string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

func (s *server) listUnreadMessages(ctx context.Context, _ *mcp.CallToolRequest, input unreadMessagesInput) (*mcp.CallToolResult, messagesOutput, error) {
	s.logAccess(ctx, "list_unread_messages")
	page, err := s.store.ListUnreadMessages(ctx, messages.UnreadParams{
		ConsumerID: input.ConsumerID, ConversationID: input.ConversationID,
		Direction: input.Direction, Type: input.Type, Order: input.Order,
		Limit: mcpLimit(input.Limit), Cursor: input.Cursor,
	})
	if err != nil {
		s.logError(ctx, "list_unread_messages", err)
		return nil, messagesOutput{}, safeToolError(err)
	}
	return nil, messagesOutput{Messages: page.Messages, NextCursor: page.NextCursor}, nil
}

type acknowledgeMessagesInput struct {
	ConsumerID string   `json:"consumer_id" jsonschema:"Required stable agent consumer ID."`
	MessageIDs []string `json:"message_ids" jsonschema:"One to 100 Contexta message IDs successfully processed by the agent."`
}

type acknowledgeMessagesOutput struct {
	Acknowledged int `json:"acknowledged"`
}

func (s *server) acknowledgeMessages(ctx context.Context, _ *mcp.CallToolRequest, input acknowledgeMessagesInput) (*mcp.CallToolResult, acknowledgeMessagesOutput, error) {
	s.logAccess(ctx, "acknowledge_messages")
	acknowledged, err := s.store.AcknowledgeMessages(ctx, input.ConsumerID, input.MessageIDs)
	if err != nil {
		s.logError(ctx, "acknowledge_messages", err)
		return nil, acknowledgeMessagesOutput{}, safeToolError(err)
	}
	return nil, acknowledgeMessagesOutput{Acknowledged: acknowledged}, nil
}

type findConversationsInput struct {
	Query     string `json:"query,omitempty" jsonschema:"Text to match against conversation title or provider ID."`
	ContactID string `json:"contact_id,omitempty" jsonschema:"Contexta contact ID."`
	Type      string `json:"type,omitempty" jsonschema:"Conversation type filter: group or direct."`
	From      string `json:"from,omitempty" jsonschema:"Inclusive RFC3339 timestamp or YYYY-MM-DD date."`
	To        string `json:"to,omitempty" jsonschema:"Exclusive RFC3339 timestamp or inclusive YYYY-MM-DD date."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum number of conversations, up to 100."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type conversationsOutput struct {
	Conversations []conversations.Conversation `json:"conversations"`
	NextCursor    string                       `json:"next_cursor,omitempty"`
}

func (s *server) findConversations(ctx context.Context, _ *mcp.CallToolRequest, input findConversationsInput) (*mcp.CallToolResult, conversationsOutput, error) {
	s.logAccess(ctx, "find_conversations")
	from, to, err := parseRange(input.From, input.To)
	if err != nil {
		return nil, conversationsOutput{}, err
	}
	page, err := s.store.ListConversations(ctx, conversations.ListParams{
		Query: input.Query, ContactID: input.ContactID, Type: input.Type, From: from, To: to,
		Limit: mcpLimit(input.Limit), Cursor: input.Cursor,
	})
	if err != nil {
		s.logError(ctx, "find_conversations", err)
		return nil, conversationsOutput{}, safeToolError(err)
	}
	return nil, conversationsOutput{Conversations: page.Conversations, NextCursor: page.NextCursor}, nil
}

type idInput struct {
	ID string `json:"id" jsonschema:"Required Contexta internal ID."`
}

type conversationOutput struct {
	Conversation conversations.Conversation `json:"conversation"`
}

func (s *server) getConversation(ctx context.Context, _ *mcp.CallToolRequest, input idInput) (*mcp.CallToolResult, conversationOutput, error) {
	s.logAccess(ctx, "get_conversation")
	conversation, err := s.store.GetConversation(ctx, input.ID)
	if err != nil {
		s.logError(ctx, "get_conversation", err)
		return nil, conversationOutput{}, safeToolError(err)
	}
	return nil, conversationOutput{Conversation: conversation}, nil
}

type getMessagesInput struct {
	ConversationID string `json:"conversation_id" jsonschema:"Required Contexta conversation ID."`
	From           string `json:"from,omitempty" jsonschema:"Inclusive RFC3339 timestamp or YYYY-MM-DD date."`
	To             string `json:"to,omitempty" jsonschema:"Exclusive RFC3339 timestamp or inclusive YYYY-MM-DD date."`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of messages, up to 100."`
	Cursor         string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

func (s *server) getMessages(ctx context.Context, _ *mcp.CallToolRequest, input getMessagesInput) (*mcp.CallToolResult, messagesOutput, error) {
	s.logAccess(ctx, "get_messages")
	from, to, err := parseRange(input.From, input.To)
	if err != nil {
		return nil, messagesOutput{}, err
	}
	page, err := s.store.SearchMessages(ctx, messages.SearchParams{
		ConversationID: input.ConversationID, From: from, To: to,
		Limit: mcpLimit(input.Limit), Cursor: input.Cursor,
	})
	if err != nil {
		s.logError(ctx, "get_messages", err)
		return nil, messagesOutput{}, safeToolError(err)
	}
	return nil, messagesOutput{Messages: page.Messages, NextCursor: page.NextCursor}, nil
}

type aroundInput struct {
	MessageID string `json:"message_id" jsonschema:"Required Contexta message ID."`
	Before    int    `json:"before,omitempty" jsonschema:"Number of preceding messages, up to 50."`
	After     int    `json:"after,omitempty" jsonschema:"Number of following messages, up to 50."`
}

type aroundOutput struct {
	Previous []messages.Message `json:"previous"`
	Message  messages.Message   `json:"message"`
	Next     []messages.Message `json:"next"`
}

func (s *server) getMessagesAround(ctx context.Context, _ *mcp.CallToolRequest, input aroundInput) (*mcp.CallToolResult, aroundOutput, error) {
	s.logAccess(ctx, "get_messages_around")
	around, err := s.store.GetMessagesAround(ctx, input.MessageID, aroundLimit(input.Before), aroundLimit(input.After))
	if err != nil {
		s.logError(ctx, "get_messages_around", err)
		return nil, aroundOutput{}, safeToolError(err)
	}
	return nil, aroundOutput{Previous: around.Previous, Message: around.Message, Next: around.Next}, nil
}

type listContactsInput struct {
	Query     string `json:"query,omitempty" jsonschema:"Name or phone fragment."`
	CompanyID string `json:"company_id,omitempty" jsonschema:"Only return contacts linked to this numeric company ID."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum number of contacts, up to 100."`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type contactsOutput struct {
	Contacts   []contacts.Contact `json:"contacts"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func (s *server) listContacts(ctx context.Context, _ *mcp.CallToolRequest, input listContactsInput) (*mcp.CallToolResult, contactsOutput, error) {
	s.logAccess(ctx, "list_contacts")
	page, err := s.store.ListContacts(ctx, contacts.ListParams{Query: input.Query, CompanyID: input.CompanyID, Limit: mcpLimit(input.Limit), Cursor: input.Cursor})
	if err != nil {
		s.logError(ctx, "list_contacts", err)
		return nil, contactsOutput{}, safeToolError(err)
	}
	return nil, contactsOutput{Contacts: page.Contacts, NextCursor: page.NextCursor}, nil
}

type contactOutput struct {
	Contact contacts.Contact `json:"contact"`
}

func (s *server) getContact(ctx context.Context, _ *mcp.CallToolRequest, input idInput) (*mcp.CallToolResult, contactOutput, error) {
	s.logAccess(ctx, "get_contact")
	contact, err := s.store.GetContact(ctx, input.ID)
	if err != nil {
		s.logError(ctx, "get_contact", err)
		return nil, contactOutput{}, safeToolError(err)
	}
	return nil, contactOutput{Contact: contact}, nil
}

type listDenylistInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of entries, up to 100."`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type denylistOutput struct {
	Entries    []denylist.Entry `json:"entries"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func (s *server) listDenylist(ctx context.Context, _ *mcp.CallToolRequest, input listDenylistInput) (*mcp.CallToolResult, denylistOutput, error) {
	s.logAccess(ctx, "list_denylist")
	page, err := s.store.ListDenylist(ctx, denylist.ListParams{Limit: mcpLimit(input.Limit), Cursor: input.Cursor})
	if err != nil {
		s.logError(ctx, "list_denylist", err)
		return nil, denylistOutput{}, safeToolError(err)
	}
	return nil, denylistOutput{Entries: page.Entries, NextCursor: page.NextCursor}, nil
}

type addDenylistInput struct {
	TargetType string `json:"target_type" jsonschema:"Required target type: conversation or contact."`
	TargetID   string `json:"target_id" jsonschema:"Required Contexta conversation or contact UUID."`
	Reason     string `json:"reason,omitempty" jsonschema:"Optional note explaining why the target is blocked."`
}

type denylistEntryOutput struct {
	Entry denylist.Entry `json:"entry"`
}

func (s *server) addToDenylist(ctx context.Context, _ *mcp.CallToolRequest, input addDenylistInput) (*mcp.CallToolResult, denylistEntryOutput, error) {
	s.logAccess(ctx, "add_to_denylist")
	entry, err := s.store.AddDenylistEntry(ctx, denylist.AddParams{
		TargetType: input.TargetType, TargetID: input.TargetID, Reason: input.Reason,
	})
	if err != nil {
		s.logError(ctx, "add_to_denylist", err)
		return nil, denylistEntryOutput{}, safeToolError(err)
	}
	return nil, denylistEntryOutput{Entry: entry}, nil
}

type removeDenylistInput struct {
	ID string `json:"id" jsonschema:"Required denylist entry ID."`
}

type removeDenylistOutput struct {
	Removed bool `json:"removed"`
}

func (s *server) removeFromDenylist(ctx context.Context, _ *mcp.CallToolRequest, input removeDenylistInput) (*mcp.CallToolResult, removeDenylistOutput, error) {
	s.logAccess(ctx, "remove_from_denylist")
	if err := s.store.RemoveDenylistEntry(ctx, input.ID); err != nil {
		s.logError(ctx, "remove_from_denylist", err)
		return nil, removeDenylistOutput{}, safeToolError(err)
	}
	return nil, removeDenylistOutput{Removed: true}, nil
}

type listTasksInput struct {
	Status         string `json:"status,omitempty" jsonschema:"Task status: pending, in_progress, blocked, done, or cancelled."`
	CompanyID      string `json:"company_id,omitempty" jsonschema:"Filter by numeric company ID."`
	Company        string `json:"company,omitempty" jsonschema:"Filter by company name substring."`
	ContactID      string `json:"contact_id,omitempty" jsonschema:"Filter by linked Contexta contact ID."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Filter by linked Contexta conversation ID."`
	ScheduleID     string `json:"schedule_id,omitempty" jsonschema:"Filter by the numeric ID of the recurring schedule that created the task."`
	Query          string `json:"query,omitempty" jsonschema:"Search text matched against title and description. Prefix with # (e.g. #12) for exact numeric task ID."`
	Overdue        bool   `json:"overdue,omitempty" jsonschema:"When true, only open tasks with due_at in the past."`
	OpenOnly       bool   `json:"open_only,omitempty" jsonschema:"When true and status is omitted, hide done and cancelled tasks."`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of tasks, up to 100."`
	Cursor         string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type tasksOutput struct {
	Tasks      []tasks.Task `json:"tasks"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

func (s *server) listTasks(ctx context.Context, _ *mcp.CallToolRequest, input listTasksInput) (*mcp.CallToolResult, tasksOutput, error) {
	s.logAccess(ctx, "list_tasks")
	page, err := s.store.ListTasks(ctx, tasks.ListParams{
		Status: input.Status, CompanyID: input.CompanyID, Company: input.Company, ContactID: input.ContactID,
		ConversationID: input.ConversationID, ScheduleID: input.ScheduleID, Query: input.Query, Overdue: input.Overdue,
		OpenOnly: input.OpenOnly, Limit: mcpLimit(input.Limit), Cursor: input.Cursor,
	})
	if err != nil {
		s.logError(ctx, "list_tasks", err)
		return nil, tasksOutput{}, safeToolError(err)
	}
	return nil, tasksOutput{Tasks: page.Tasks, NextCursor: page.NextCursor}, nil
}

type taskIDInput struct {
	ID string `json:"id" jsonschema:"Required numeric Contexta task ID (e.g. 1, 2, 3)."`
}

type taskOutput struct {
	Task tasks.Task `json:"task"`
}

func (s *server) getTask(ctx context.Context, _ *mcp.CallToolRequest, input taskIDInput) (*mcp.CallToolResult, taskOutput, error) {
	s.logAccess(ctx, "get_task")
	task, err := s.store.GetTask(ctx, input.ID)
	if err != nil {
		s.logError(ctx, "get_task", err)
		return nil, taskOutput{}, safeToolError(err)
	}
	return nil, taskOutput{Task: task}, nil
}

type createTaskInput struct {
	Title          string `json:"title" jsonschema:"Required task title."`
	Description    string `json:"description,omitempty" jsonschema:"Optional task description."`
	CompanyID      string `json:"company_id,omitempty" jsonschema:"Optional numeric company ID. Defaults to the linked contact's company, if any."`
	Status         string `json:"status,omitempty" jsonschema:"pending, in_progress, blocked, done, or cancelled. Defaults to pending."`
	DueAt          string `json:"due_at,omitempty" jsonschema:"Optional due date as RFC3339 or YYYY-MM-DD."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Optional Contexta conversation UUID to link."`
	ContactID      string `json:"contact_id,omitempty" jsonschema:"Optional Contexta contact UUID to link."`
}

func (s *server) createTask(ctx context.Context, _ *mcp.CallToolRequest, input createTaskInput) (*mcp.CallToolResult, taskOutput, error) {
	s.logAccess(ctx, "create_task")
	dueAt, err := parseDateTime(input.DueAt, false)
	if err != nil {
		return nil, taskOutput{}, errors.New("invalid due_at; expected RFC3339 or YYYY-MM-DD")
	}
	task, err := s.store.CreateTask(ctx, tasks.CreateParams{
		Title: input.Title, Description: input.Description, CompanyID: input.CompanyID,
		Status: input.Status, DueAt: dueAt,
		ConversationID: input.ConversationID, ContactID: input.ContactID,
	})
	if err != nil {
		s.logError(ctx, "create_task", err)
		return nil, taskOutput{}, safeToolError(err)
	}
	return nil, taskOutput{Task: task}, nil
}

type updateTaskInput struct {
	ID             string  `json:"id" jsonschema:"Required numeric Contexta task ID (e.g. 1, 2, 3)."`
	Title          *string `json:"title,omitempty" jsonschema:"New title."`
	Description    *string `json:"description,omitempty" jsonschema:"New description."`
	CompanyID      *string `json:"company_id,omitempty" jsonschema:"Linked numeric company ID. Empty string clears it."`
	Status         *string `json:"status,omitempty" jsonschema:"pending, in_progress, blocked, done, or cancelled."`
	DueAt          *string `json:"due_at,omitempty" jsonschema:"New due date as RFC3339 or YYYY-MM-DD. Empty string clears it."`
	ConversationID *string `json:"conversation_id,omitempty" jsonschema:"Linked conversation UUID. Empty string clears it."`
	ContactID      *string `json:"contact_id,omitempty" jsonschema:"Linked contact UUID. Empty string clears it."`
	DeleteMemories bool    `json:"delete_memories,omitempty" jsonschema:"Only when the resulting status is done or cancelled: permanently delete memories linked only to this task. Memories linked to other tasks are kept. Defaults to false."`
}

type updateTaskOutput struct {
	Task tasks.Task `json:"task"`
	tasks.MemoryCleanup
}

func (s *server) updateTask(ctx context.Context, _ *mcp.CallToolRequest, input updateTaskInput) (*mcp.CallToolResult, updateTaskOutput, error) {
	s.logAccess(ctx, "update_task")
	task, cleanup, err := s.store.UpdateTask(ctx, input.ID, tasks.UpdateParams{
		Title: input.Title, Description: input.Description, CompanyID: input.CompanyID,
		Status: input.Status, DueAt: input.DueAt,
		ConversationID: input.ConversationID, ContactID: input.ContactID,
		DeleteMemories: input.DeleteMemories,
	})
	if err != nil {
		s.logError(ctx, "update_task", err)
		return nil, updateTaskOutput{}, safeToolError(err)
	}
	return nil, updateTaskOutput{Task: task, MemoryCleanup: cleanup}, nil
}

type deleteTaskInput struct {
	ID             string `json:"id" jsonschema:"Required numeric Contexta task ID (e.g. 1, 2, 3)."`
	DeleteMemories bool   `json:"delete_memories,omitempty" jsonschema:"Also permanently delete memories linked only to this task. Memories linked to other tasks are kept. Defaults to false."`
}

func (s *server) deleteTask(ctx context.Context, _ *mcp.CallToolRequest, input deleteTaskInput) (*mcp.CallToolResult, tasks.DeleteResult, error) {
	s.logAccess(ctx, "delete_task")
	result, err := s.store.DeleteTask(ctx, input.ID, input.DeleteMemories)
	if err != nil {
		s.logError(ctx, "delete_task", err)
		return nil, tasks.DeleteResult{}, safeToolError(err)
	}
	return nil, result, nil
}

type attachMemoryToTaskInput struct {
	TaskID   string `json:"task_id" jsonschema:"Required numeric Contexta task ID (e.g. 1, 2, 3)."`
	MemoryID string `json:"memory_id" jsonschema:"Required Contexta memory UUID to link."`
}

func (s *server) attachMemoryToTask(ctx context.Context, _ *mcp.CallToolRequest, input attachMemoryToTaskInput) (*mcp.CallToolResult, taskOutput, error) {
	s.logAccess(ctx, "attach_memory_to_task")
	task, err := s.store.AttachTaskMemory(ctx, input.TaskID, input.MemoryID)
	if err != nil {
		s.logError(ctx, "attach_memory_to_task", err)
		return nil, taskOutput{}, safeToolError(err)
	}
	return nil, taskOutput{Task: task}, nil
}

type detachMemoryFromTaskInput struct {
	TaskID   string `json:"task_id" jsonschema:"Required numeric Contexta task ID (e.g. 1, 2, 3)."`
	MemoryID string `json:"memory_id" jsonschema:"Required Contexta memory UUID to unlink."`
}

func (s *server) detachMemoryFromTask(ctx context.Context, _ *mcp.CallToolRequest, input detachMemoryFromTaskInput) (*mcp.CallToolResult, taskOutput, error) {
	s.logAccess(ctx, "detach_memory_from_task")
	task, err := s.store.DetachTaskMemory(ctx, input.TaskID, input.MemoryID)
	if err != nil {
		s.logError(ctx, "detach_memory_from_task", err)
		return nil, taskOutput{}, safeToolError(err)
	}
	return nil, taskOutput{Task: task}, nil
}

type searchMemoriesInput struct {
	Query          string `json:"query" jsonschema:"Required natural-language query for semantic recall."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Optional Contexta conversation UUID filter."`
	ContactID      string `json:"contact_id,omitempty" jsonschema:"Optional Contexta contact UUID filter."`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum hits, up to 50."`
}

type memoriesSearchOutput struct {
	Hits []memories.SearchHit `json:"hits"`
}

func (s *server) searchMemories(ctx context.Context, _ *mcp.CallToolRequest, input searchMemoriesInput) (*mcp.CallToolResult, memoriesSearchOutput, error) {
	s.logAccess(ctx, "search_memories")
	if s.memories == nil {
		return nil, memoriesSearchOutput{}, errors.New("memories unavailable")
	}
	result, err := s.memories.Search(ctx, memories.SearchParams{
		Query: input.Query, ConversationID: input.ConversationID, ContactID: input.ContactID, Limit: input.Limit,
	})
	if err != nil {
		s.logError(ctx, "search_memories", err)
		return nil, memoriesSearchOutput{}, safeToolError(err)
	}
	return nil, memoriesSearchOutput{Hits: result.Hits}, nil
}

type listMemoriesInput struct {
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Filter by linked Contexta conversation ID."`
	ContactID      string `json:"contact_id,omitempty" jsonschema:"Filter by linked Contexta contact ID."`
	Source         string `json:"source,omitempty" jsonschema:"note or message."`
	Query          string `json:"query,omitempty" jsonschema:"Substring search against title and content."`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of memories, up to 100."`
	Cursor         string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type memoriesOutput struct {
	Memories   []memories.Memory `json:"memories"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func (s *server) listMemories(ctx context.Context, _ *mcp.CallToolRequest, input listMemoriesInput) (*mcp.CallToolResult, memoriesOutput, error) {
	s.logAccess(ctx, "list_memories")
	if s.memories == nil {
		return nil, memoriesOutput{}, errors.New("memories unavailable")
	}
	page, err := s.memories.List(ctx, memories.ListParams{
		ConversationID: input.ConversationID, ContactID: input.ContactID, Source: input.Source,
		Query: input.Query, Limit: mcpLimit(input.Limit), Cursor: input.Cursor,
	})
	if err != nil {
		s.logError(ctx, "list_memories", err)
		return nil, memoriesOutput{}, safeToolError(err)
	}
	return nil, memoriesOutput{Memories: page.Memories, NextCursor: page.NextCursor}, nil
}

type memoryOutput struct {
	Memory memories.Memory `json:"memory"`
}

func (s *server) getMemory(ctx context.Context, _ *mcp.CallToolRequest, input idInput) (*mcp.CallToolResult, memoryOutput, error) {
	s.logAccess(ctx, "get_memory")
	if s.memories == nil {
		return nil, memoryOutput{}, errors.New("memories unavailable")
	}
	memory, err := s.memories.Get(ctx, input.ID)
	if err != nil {
		s.logError(ctx, "get_memory", err)
		return nil, memoryOutput{}, safeToolError(err)
	}
	return nil, memoryOutput{Memory: memory}, nil
}

type saveMemoryInput struct {
	Title          string `json:"title,omitempty" jsonschema:"Optional memory title."`
	Content        string `json:"content,omitempty" jsonschema:"Free-text content. Required unless message_id is provided."`
	MessageID      string `json:"message_id,omitempty" jsonschema:"Optional Contexta message UUID to persist as a memory. Loads text/transcription when content is empty."`
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"Optional Contexta conversation UUID to link."`
	ContactID      string `json:"contact_id,omitempty" jsonschema:"Optional Contexta contact UUID to link."`
}

func (s *server) saveMemory(ctx context.Context, _ *mcp.CallToolRequest, input saveMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
	s.logAccess(ctx, "save_memory")
	if s.memories == nil {
		return nil, memoryOutput{}, errors.New("memories unavailable")
	}
	memory, err := s.memories.Create(ctx, memories.CreateParams{
		Title: input.Title, Content: input.Content, MessageID: input.MessageID,
		ConversationID: input.ConversationID, ContactID: input.ContactID,
	})
	if err != nil {
		s.logError(ctx, "save_memory", err)
		return nil, memoryOutput{}, safeToolError(err)
	}
	return nil, memoryOutput{Memory: memory}, nil
}

type updateMemoryInput struct {
	ID             string  `json:"id" jsonschema:"Required Contexta memory ID."`
	Title          *string `json:"title,omitempty" jsonschema:"New title."`
	Content        *string `json:"content,omitempty" jsonschema:"New content. Re-embeds when changed."`
	ConversationID *string `json:"conversation_id,omitempty" jsonschema:"Linked conversation UUID. Empty string clears it."`
	ContactID      *string `json:"contact_id,omitempty" jsonschema:"Linked contact UUID. Empty string clears it."`
}

func (s *server) updateMemory(ctx context.Context, _ *mcp.CallToolRequest, input updateMemoryInput) (*mcp.CallToolResult, memoryOutput, error) {
	s.logAccess(ctx, "update_memory")
	if s.memories == nil {
		return nil, memoryOutput{}, errors.New("memories unavailable")
	}
	memory, err := s.memories.Update(ctx, input.ID, memories.UpdateParams{
		Title: input.Title, Content: input.Content,
		ConversationID: input.ConversationID, ContactID: input.ContactID,
	})
	if err != nil {
		s.logError(ctx, "update_memory", err)
		return nil, memoryOutput{}, safeToolError(err)
	}
	return nil, memoryOutput{Memory: memory}, nil
}

type deleteMemoryInput struct {
	ID string `json:"id" jsonschema:"Required Contexta memory ID."`
}

type deleteMemoryOutput struct {
	Deleted bool `json:"deleted"`
}

func (s *server) deleteMemory(ctx context.Context, _ *mcp.CallToolRequest, input deleteMemoryInput) (*mcp.CallToolResult, deleteMemoryOutput, error) {
	s.logAccess(ctx, "delete_memory")
	if s.memories == nil {
		return nil, deleteMemoryOutput{}, errors.New("memories unavailable")
	}
	if err := s.memories.Delete(ctx, input.ID); err != nil {
		s.logError(ctx, "delete_memory", err)
		return nil, deleteMemoryOutput{}, safeToolError(err)
	}
	return nil, deleteMemoryOutput{Deleted: true}, nil
}

func (s *server) logError(ctx context.Context, tool string, err error) {
	s.logger.Error("mcp tool failed", "tool", tool, "error", err)
	if recordErr := s.store.RecordActivity(ctx, activity.Record{Category: "mcp", Level: "error", Operation: tool, Outcome: "failed"}); recordErr != nil {
		s.logger.Error("activity recording failed", "category", "mcp", "operation", tool, "error", recordErr)
	}
}

func (s *server) logAccess(ctx context.Context, tool string) {
	s.logger.Info("mcp tool called", "tool", tool)
	if err := s.store.RecordActivity(ctx, activity.Record{Category: "mcp", Level: "info", Operation: tool, Outcome: "called"}); err != nil {
		s.logger.Error("activity recording failed", "category", "mcp", "operation", tool, "error", err)
	}
}

func safeToolError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, postgres.ErrNotFound) {
		return errors.New("not found")
	}
	if errors.Is(err, postgres.ErrInvalidArgument) || errors.Is(err, pagination.ErrInvalidCursor) ||
		errors.Is(err, memories.ErrInvalidContent) || errors.Is(err, memories.ErrEmbedderUnavailable) {
		return errors.New("invalid argument")
	}
	return errors.New("query failed")
}

func parseRange(fromValue, toValue string) (*time.Time, *time.Time, error) {
	from, err := parseDateTime(fromValue, false)
	if err != nil {
		return nil, nil, errors.New("invalid from; expected RFC3339 or YYYY-MM-DD")
	}
	to, err := parseDateTime(toValue, true)
	if err != nil {
		return nil, nil, errors.New("invalid to; expected RFC3339 or YYYY-MM-DD")
	}
	return from, to, nil
}

func parseDateTime(value string, dateEnd bool) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		parsed = parsed.UTC()
		return &parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	if dateEnd {
		parsed = parsed.AddDate(0, 0, 1)
	}
	return &parsed, nil
}

func mcpLimit(value int) int {
	if value <= 0 {
		return 20
	}
	return min(value, 100)
}

func aroundLimit(value int) int {
	if value <= 0 {
		return 10
	}
	return min(value, 50)
}
