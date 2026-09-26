package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/auth"
	"github.com/leofelipet/contexta/internal/contacts"
	"github.com/leofelipet/contexta/internal/conversations"
	"github.com/leofelipet/contexta/internal/denylist"
	"github.com/leofelipet/contexta/internal/messages"
	"github.com/leofelipet/contexta/internal/pagination"
	"github.com/leofelipet/contexta/internal/storage/postgres"
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
	RecordActivity(context.Context, activity.Record) error
}

type server struct {
	store  Store
	logger *slog.Logger
}

func New(store Store, token string, logger *slog.Logger) http.Handler {
	implementation := &server{store: store, logger: logger}
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
	mcp.AddTool(mcpServer, readOnlyTool("list_contacts", "List or search stored WhatsApp contacts."), s.listContacts)
	mcp.AddTool(mcpServer, readOnlyTool("get_contact", "Get one contact by its Contexta contact ID."), s.getContact)
	mcp.AddTool(mcpServer, readOnlyTool("list_denylist", "List conversations and contacts blocked from message ingestion."), s.listDenylist)
	mcp.AddTool(mcpServer, localWriteTool("add_to_denylist", "Block future message ingestion for a conversation (e.g. group) or contact (direct chat only)."), s.addToDenylist)
	mcp.AddTool(mcpServer, localWriteTool("remove_from_denylist", "Remove a denylist entry so messages from that target are ingested again."), s.removeFromDenylist)
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
		Query: input.Query, ContactID: input.ContactID, From: from, To: to,
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
	Query  string `json:"query,omitempty" jsonschema:"Name or phone fragment."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of contacts, up to 100."`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type contactsOutput struct {
	Contacts   []contacts.Contact `json:"contacts"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

func (s *server) listContacts(ctx context.Context, _ *mcp.CallToolRequest, input listContactsInput) (*mcp.CallToolResult, contactsOutput, error) {
	s.logAccess(ctx, "list_contacts")
	page, err := s.store.ListContacts(ctx, contacts.ListParams{Query: input.Query, Limit: mcpLimit(input.Limit), Cursor: input.Cursor})
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
	if errors.Is(err, postgres.ErrInvalidArgument) || errors.Is(err, pagination.ErrInvalidCursor) {
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
