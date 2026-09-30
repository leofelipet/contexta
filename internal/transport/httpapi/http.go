package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/admin"
	"github.com/leofelipet/contexta/internal/auth"
	"github.com/leofelipet/contexta/internal/companies"
	"github.com/leofelipet/contexta/internal/contacts"
	"github.com/leofelipet/contexta/internal/conversations"
	"github.com/leofelipet/contexta/internal/denylist"
	"github.com/leofelipet/contexta/internal/email"
	"github.com/leofelipet/contexta/internal/ingestion"
	"github.com/leofelipet/contexta/internal/memories"
	"github.com/leofelipet/contexta/internal/messages"
	"github.com/leofelipet/contexta/internal/pagination"
	"github.com/leofelipet/contexta/internal/providers/whatsapp/uazapi"
	"github.com/leofelipet/contexta/internal/schedules"
	"github.com/leofelipet/contexta/internal/storage/postgres"
	"github.com/leofelipet/contexta/internal/tasks"
	"github.com/leofelipet/contexta/internal/version"
)

const (
	maxWebhookBody  = 2 << 20
	maxAPIWriteBody = 1 << 20
)

type Store interface {
	Ping(context.Context) error
	ListContacts(context.Context, contacts.ListParams) (contacts.Page, error)
	GetContact(context.Context, string) (contacts.Contact, error)
	ListConversations(context.Context, conversations.ListParams) (conversations.Page, error)
	ListStaleConversations(context.Context, conversations.StaleParams) (conversations.StalePage, error)
	GetConversation(context.Context, string) (conversations.Conversation, error)
	DeleteConversation(context.Context, string) (conversations.DeleteResult, error)
	DeleteConversations(context.Context, []string) (conversations.BulkDeleteResult, error)
	SearchMessages(context.Context, messages.SearchParams) (messages.Page, error)
	ListUnreadMessages(context.Context, messages.UnreadParams) (messages.Page, error)
	AcknowledgeMessages(context.Context, string, []string) (int, error)
	GetMessage(context.Context, string) (messages.Message, error)
	GetMessagesAround(context.Context, string, int, int) (messages.Around, error)
	Dashboard(context.Context) (admin.Dashboard, error)
	LastActivityAt(context.Context, string) (*time.Time, error)
	RecordActivity(context.Context, activity.Record) error
	ListActivity(context.Context, activity.ListParams) (activity.Page, error)
	ListDenylist(context.Context, denylist.ListParams) (denylist.Page, error)
	AddDenylistEntry(context.Context, denylist.AddParams) (denylist.Entry, error)
	RemoveDenylistEntry(context.Context, string) error
	ListCompanies(context.Context, companies.ListParams) (companies.Page, error)
	GetCompany(context.Context, string) (companies.Company, error)
	CreateCompany(context.Context, companies.CreateParams) (companies.Company, error)
	UpdateCompany(context.Context, string, companies.UpdateParams) (companies.Company, error)
	DeleteCompany(context.Context, string) error
	AttachContactToCompany(ctx context.Context, companyID, contactID string) (companies.Company, error)
	DetachContactFromCompany(ctx context.Context, companyID, contactID string) (companies.Company, error)
	ListTasks(context.Context, tasks.ListParams) (tasks.Page, error)
	GetTask(context.Context, string) (tasks.Task, error)
	CreateTask(context.Context, tasks.CreateParams) (tasks.Task, error)
	UpdateTask(context.Context, string, tasks.UpdateParams) (tasks.Task, tasks.MemoryCleanup, error)
	DeleteTask(ctx context.Context, id string, deleteMemories bool) (tasks.DeleteResult, error)
	AttachTaskMemory(ctx context.Context, taskID, memoryID string) (tasks.Task, error)
	DetachTaskMemory(ctx context.Context, taskID, memoryID string) (tasks.Task, error)
	ListTaskSchedules(context.Context, schedules.ListParams) (schedules.Page, error)
	GetTaskSchedule(context.Context, string) (schedules.Schedule, error)
	CreateTaskSchedule(context.Context, schedules.CreateParams) (schedules.Schedule, error)
	UpdateTaskSchedule(context.Context, string, schedules.UpdateParams) (schedules.Schedule, error)
	DeleteTaskSchedule(context.Context, string) error
	SystemOverview(context.Context) (admin.SystemOverview, error)
}

type UAZAPIClient interface {
	Status(context.Context) (uazapi.InstanceStatus, error)
	Webhooks(context.Context) ([]uazapi.Webhook, error)
	ConfigureWebhook(context.Context, string) error
}

type Options struct {
	Store              Store
	Memories           *memories.Service
	Email              *email.Service
	Ingestion          *ingestion.Service
	APIToken           string
	WebhookSecret      string
	ProviderInstanceID string
	CaptureDir         string
	WebhookPublicURL   string
	UAZAPIClient       UAZAPIClient
	MCPEnabled         bool
	StartedAt          time.Time
	Logger             *slog.Logger
}

func New(options Options) http.Handler {
	startedAt := options.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	handler := &handler{
		store:              options.Store,
		memories:           options.Memories,
		email:              options.Email,
		ingestion:          options.Ingestion,
		webhookSecret:      options.WebhookSecret,
		providerInstanceID: options.ProviderInstanceID,
		captureDir:         options.CaptureDir,
		webhookPublicURL:   options.WebhookPublicURL,
		uazapi:             options.UAZAPIClient,
		mcpEnabled:         options.MCPEnabled,
		startedAt:          startedAt,
		logger:             options.Logger,
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /health/live", handler.live)
	root.HandleFunc("GET /health/ready", handler.ready)
	root.HandleFunc("POST /webhooks/uazapi/{secret}", handler.uazapiWebhook)

	api := http.NewServeMux()
	api.HandleFunc("GET /api/v1/contacts", handler.listContacts)
	api.HandleFunc("GET /api/v1/contacts/{id}", handler.getContact)
	api.HandleFunc("GET /api/v1/conversations", handler.listConversations)
	api.HandleFunc("GET /api/v1/conversations/stale", handler.listStaleConversations)
	api.HandleFunc("POST /api/v1/conversations/bulk-delete", handler.bulkDeleteConversations)
	api.HandleFunc("GET /api/v1/conversations/{id}", handler.getConversation)
	api.HandleFunc("DELETE /api/v1/conversations/{id}", handler.deleteConversation)
	api.HandleFunc("GET /api/v1/conversations/{id}/messages", handler.conversationMessages)
	api.HandleFunc("GET /api/v1/messages", handler.searchMessages)
	api.HandleFunc("GET /api/v1/messages/unread", handler.listUnreadMessages)
	api.HandleFunc("POST /api/v1/messages/acknowledge", handler.acknowledgeMessages)
	api.HandleFunc("GET /api/v1/messages/{id}", handler.getMessage)
	api.HandleFunc("GET /api/v1/messages/{id}/around", handler.messagesAround)
	api.HandleFunc("GET /api/v1/dashboard", handler.dashboard)
	api.HandleFunc("GET /api/v1/system", handler.system)
	api.HandleFunc("GET /api/v1/denylist", handler.listDenylist)
	api.HandleFunc("POST /api/v1/denylist", handler.addDenylistEntry)
	api.HandleFunc("DELETE /api/v1/denylist/{id}", handler.removeDenylistEntry)
	api.HandleFunc("GET /api/v1/companies", handler.listCompanies)
	api.HandleFunc("POST /api/v1/companies", handler.createCompany)
	api.HandleFunc("GET /api/v1/companies/{id}", handler.getCompany)
	api.HandleFunc("PATCH /api/v1/companies/{id}", handler.updateCompany)
	api.HandleFunc("DELETE /api/v1/companies/{id}", handler.deleteCompany)
	api.HandleFunc("POST /api/v1/companies/{id}/contacts", handler.attachCompanyContact)
	api.HandleFunc("DELETE /api/v1/companies/{id}/contacts/{contact_id}", handler.detachCompanyContact)
	api.HandleFunc("GET /api/v1/tasks", handler.listTasks)
	api.HandleFunc("GET /api/v1/tasks/{id}", handler.getTask)
	api.HandleFunc("POST /api/v1/tasks", handler.createTask)
	api.HandleFunc("PATCH /api/v1/tasks/{id}", handler.updateTask)
	api.HandleFunc("DELETE /api/v1/tasks/{id}", handler.deleteTask)
	api.HandleFunc("POST /api/v1/tasks/{id}/memories", handler.attachTaskMemory)
	api.HandleFunc("DELETE /api/v1/tasks/{id}/memories/{memory_id}", handler.detachTaskMemory)
	api.HandleFunc("GET /api/v1/task-schedules", handler.listTaskSchedules)
	api.HandleFunc("POST /api/v1/task-schedules", handler.createTaskSchedule)
	api.HandleFunc("GET /api/v1/task-schedules/{id}", handler.getTaskSchedule)
	api.HandleFunc("PATCH /api/v1/task-schedules/{id}", handler.updateTaskSchedule)
	api.HandleFunc("DELETE /api/v1/task-schedules/{id}", handler.deleteTaskSchedule)
	api.HandleFunc("GET /api/v1/memories", handler.listMemories)
	api.HandleFunc("POST /api/v1/memories/search", handler.searchMemories)
	api.HandleFunc("GET /api/v1/memories/{id}", handler.getMemory)
	api.HandleFunc("POST /api/v1/memories", handler.createMemory)
	api.HandleFunc("PATCH /api/v1/memories/{id}", handler.updateMemory)
	api.HandleFunc("DELETE /api/v1/memories/{id}", handler.deleteMemory)
	api.HandleFunc("GET /api/v1/integrations/uazapi", handler.uazapiStatus)
	api.HandleFunc("POST /api/v1/integrations/uazapi/configure-webhook", handler.configureUAZAPIWebhook)
	api.HandleFunc("GET /api/v1/email-accounts", handler.listEmailAccounts)
	api.HandleFunc("POST /api/v1/email-accounts", handler.createEmailAccount)
	api.HandleFunc("GET /api/v1/email-accounts/{id}", handler.getEmailAccount)
	api.HandleFunc("PATCH /api/v1/email-accounts/{id}", handler.updateEmailAccount)
	api.HandleFunc("DELETE /api/v1/email-accounts/{id}", handler.deleteEmailAccount)
	api.HandleFunc("POST /api/v1/email-accounts/{id}/test", handler.testEmailAccount)
	api.HandleFunc("GET /api/v1/mcp/status", handler.mcpStatus)
	api.HandleFunc("GET /api/v1/activity", handler.listActivity)
	root.Handle("/api/v1/", auth.NewMiddleware(options.APIToken).Wrap(api))

	return accessLog(options.Logger, recoverPanic(options.Logger, root))
}

type handler struct {
	store              Store
	memories           *memories.Service
	email              *email.Service
	ingestion          *ingestion.Service
	webhookSecret      string
	providerInstanceID string
	captureDir         string
	webhookPublicURL   string
	uazapi             UAZAPIClient
	mcpEnabled         bool
	startedAt          time.Time
	logger             *slog.Logger
}

func (h *handler) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *handler) uazapiWebhook(w http.ResponseWriter, r *http.Request) {
	if !auth.SecureEqual(r.PathValue("secret"), h.webhookSecret) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	if err := uazapi.Capture(h.captureDir, payload); err != nil {
		h.logger.Error("webhook capture failed", "provider", "uazapi", "error", err)
	}
	event, err := uazapi.DecodeEvent(payload)
	if errors.Is(err, uazapi.ErrUnsupportedEvent) {
		h.logger.Info("webhook event ignored", "provider", "uazapi", "reason", err.Error())
		h.recordActivity(r.Context(), activity.Record{Category: "webhook", Level: "info", Operation: "webhook_received", Outcome: "ignored"})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		h.logger.Warn("webhook validation failed", "provider", "uazapi", "error", err)
		h.recordActivity(r.Context(), activity.Record{Category: "webhook", Level: "warning", Operation: "webhook_received", Outcome: "invalid"})
		writeError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	if len(event.Messages) == 0 {
		h.logger.Info("webhook event processed", "provider", "uazapi", "event_type", event.Type, "messages", 0)
		h.recordActivity(r.Context(), activity.Record{Category: "webhook", Level: "info", Operation: "webhook_processed", Outcome: "success"})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	result, err := h.ingestion.Ingest(r.Context(), ingestion.Batch{
		Provider: "uazapi", ProviderInstanceID: h.providerInstanceID, Messages: event.Messages,
	})
	if err != nil {
		h.logger.Error("webhook processing failed", "provider", "uazapi", "event_type", event.Type, "error", err)
		h.recordActivity(r.Context(), activity.Record{Category: "webhook", Level: "error", Operation: "webhook_processed", Outcome: "failed"})
		writeError(w, http.StatusInternalServerError, "webhook processing failed")
		return
	}
	h.logger.Info("webhook event processed", "provider", "uazapi", "event_type", event.Type,
		"processed", result.Processed, "created", result.Created, "updated", result.Updated)
	metadata, _ := json.Marshal(map[string]int{"processed": result.Processed, "created": result.Created, "updated": result.Updated})
	h.recordActivity(r.Context(), activity.Record{Category: "webhook", Level: "info", Operation: "webhook_processed", Outcome: "success", Metadata: metadata})
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listContacts(w http.ResponseWriter, r *http.Request) {
	page, err := h.store.ListContacts(r.Context(), contacts.ListParams{
		Query: r.URL.Query().Get("query"), CompanyID: r.URL.Query().Get("company_id"),
		Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[contacts.Contact]{Data: page.Contacts, NextCursor: page.NextCursor})
}

func (h *handler) getContact(w http.ResponseWriter, r *http.Request) {
	contact, err := h.store.GetContact(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, contact)
}

func (h *handler) listConversations(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := h.store.ListConversations(r.Context(), conversations.ListParams{
		Query: r.URL.Query().Get("query"), ContactID: r.URL.Query().Get("contact_id"),
		Type: r.URL.Query().Get("type"),
		From: from, To: to, Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[conversations.Conversation]{Data: page.Conversations, NextCursor: page.NextCursor})
}

func (h *handler) listStaleConversations(w http.ResponseWriter, r *http.Request) {
	page, err := h.store.ListStaleConversations(r.Context(), conversations.StaleParams{
		Days:  parseIntDefault(r.URL.Query().Get("days"), 30),
		Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": page.Conversations, "next_cursor": page.NextCursor, "days": page.Days,
	})
}

func (h *handler) getConversation(w http.ResponseWriter, r *http.Request) {
	conversation, err := h.store.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversation)
}

func (h *handler) deleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	result, err := h.store.DeleteConversation(r.Context(), id)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "conversation_deleted", Outcome: "success",
		EntityType: "conversation", EntityID: id,
	})
	writeJSON(w, http.StatusOK, result)
}

type bulkDeleteConversationsRequest struct {
	IDs []string `json:"ids"`
}

func (h *handler) bulkDeleteConversations(w http.ResponseWriter, r *http.Request) {
	var request bulkDeleteConversationsRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.store.DeleteConversations(r.Context(), request.IDs)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "conversations_bulk_deleted", Outcome: "success",
		EntityType: "conversation",
	})
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) conversationMessages(w http.ResponseWriter, r *http.Request) {
	h.searchMessagesWithConversation(w, r, r.PathValue("id"))
}

func (h *handler) searchMessages(w http.ResponseWriter, r *http.Request) {
	h.searchMessagesWithConversation(w, r, r.URL.Query().Get("conversation_id"))
}

func (h *handler) searchMessagesWithConversation(w http.ResponseWriter, r *http.Request, conversationID string) {
	from, to, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := h.store.SearchMessages(r.Context(), messages.SearchParams{
		Query: r.URL.Query().Get("query"), From: from, To: to,
		ContactID: r.URL.Query().Get("contact_id"), ConversationID: conversationID,
		Direction: r.URL.Query().Get("direction"), Type: r.URL.Query().Get("type"),
		Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
		ConsumerID: r.URL.Query().Get("consumer_id"), ReadState: r.URL.Query().Get("read_state"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[messages.Message]{Data: page.Messages, NextCursor: page.NextCursor})
}

func (h *handler) listUnreadMessages(w http.ResponseWriter, r *http.Request) {
	page, err := h.store.ListUnreadMessages(r.Context(), messages.UnreadParams{
		ConsumerID: r.URL.Query().Get("consumer_id"), ConversationID: r.URL.Query().Get("conversation_id"),
		Direction: r.URL.Query().Get("direction"), Type: r.URL.Query().Get("type"),
		Order: r.URL.Query().Get("order"), Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[messages.Message]{Data: page.Messages, NextCursor: page.NextCursor})
}

func (h *handler) acknowledgeMessages(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ConsumerID string   `json:"consumer_id"`
		MessageIDs []string `json:"message_ids"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	acknowledged, err := h.store.AcknowledgeMessages(r.Context(), request.ConsumerID, request.MessageIDs)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{Category: "agent", Level: "info", Operation: "messages_acknowledged", Outcome: "success"})
	writeJSON(w, http.StatusOK, map[string]int{"acknowledged": acknowledged})
}

func (h *handler) getMessage(w http.ResponseWriter, r *http.Request) {
	message, err := h.store.GetMessage(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, message)
}

func (h *handler) messagesAround(w http.ResponseWriter, r *http.Request) {
	around, err := h.store.GetMessagesAround(r.Context(), r.PathValue("id"),
		parseIntDefault(r.URL.Query().Get("before"), 10), parseIntDefault(r.URL.Query().Get("after"), 10))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, around)
}

func (h *handler) dashboard(w http.ResponseWriter, r *http.Request) {
	dashboard, err := h.store.Dashboard(r.Context())
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	dashboard.Version = version.Version
	dashboard.UAZAPIStatus = "unavailable"
	if h.uazapi != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if status, err := h.uazapi.Status(ctx); err == nil {
			dashboard.UAZAPIStatus = status.Instance.Status
		}
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (h *handler) system(w http.ResponseWriter, r *http.Request) {
	overview, err := h.store.SystemOverview(r.Context())
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	overview.GeneratedAt = time.Now().UTC()
	overview.Version = version.Version
	overview.StartedAt = h.startedAt
	writeJSON(w, http.StatusOK, overview)
}

func (h *handler) listDenylist(w http.ResponseWriter, r *http.Request) {
	page, err := h.store.ListDenylist(r.Context(), denylist.ListParams{
		Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[denylist.Entry]{Data: page.Entries, NextCursor: page.NextCursor})
}

func (h *handler) addDenylistEntry(w http.ResponseWriter, r *http.Request) {
	var request denylist.AddParams
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	entry, err := h.store.AddDenylistEntry(r.Context(), request)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "denylist_added", Outcome: "success",
		EntityType: "denylist", EntityID: entry.ID,
	})
	writeJSON(w, http.StatusOK, entry)
}

func (h *handler) removeDenylistEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.RemoveDenylistEntry(r.Context(), id); err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "denylist_removed", Outcome: "success",
		EntityType: "denylist", EntityID: id,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

func (h *handler) listTasks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, err := h.store.ListTasks(r.Context(), tasks.ListParams{
		Status:         query.Get("status"),
		CompanyID:      query.Get("company_id"),
		Company:        query.Get("company"),
		ContactID:      query.Get("contact_id"),
		ConversationID: query.Get("conversation_id"),
		ScheduleID:     query.Get("schedule_id"),
		Query:          query.Get("q"),
		Overdue:        query.Get("overdue") == "1" || strings.EqualFold(query.Get("overdue"), "true"),
		OpenOnly:       query.Get("open_only") == "1" || strings.EqualFold(query.Get("open_only"), "true"),
		Limit:          parseLimit(r),
		Cursor:         query.Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[tasks.Task]{Data: page.Tasks, NextCursor: page.NextCursor})
}

func (h *handler) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := h.store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

type createTaskRequest struct {
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	CompanyID      string  `json:"company_id"`
	Status         string  `json:"status"`
	DueAt          *string `json:"due_at"`
	ConversationID string  `json:"conversation_id"`
	ContactID      string  `json:"contact_id"`
}

func (h *handler) createTask(w http.ResponseWriter, r *http.Request) {
	var request createTaskRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dueAt, err := parseOptionalDueAt(request.DueAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid due_at; expected RFC3339 or YYYY-MM-DD")
		return
	}
	task, err := h.store.CreateTask(r.Context(), tasks.CreateParams{
		Title: request.Title, Description: request.Description, CompanyID: request.CompanyID,
		Status: request.Status, DueAt: dueAt,
		ConversationID: request.ConversationID, ContactID: request.ContactID,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "task_created", Outcome: "success",
		EntityType: "task", EntityID: task.ID,
	})
	writeJSON(w, http.StatusOK, task)
}

type updateTaskRequest struct {
	Title          *string `json:"title"`
	Description    *string `json:"description"`
	CompanyID      *string `json:"company_id"`
	Status         *string `json:"status"`
	DueAt          *string `json:"due_at"`
	ConversationID *string `json:"conversation_id"`
	ContactID      *string `json:"contact_id"`
	DeleteMemories bool    `json:"delete_memories"`
}

func (h *handler) updateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var request updateTaskRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	task, _, err := h.store.UpdateTask(r.Context(), id, tasks.UpdateParams{
		Title: request.Title, Description: request.Description, CompanyID: request.CompanyID,
		Status: request.Status, DueAt: request.DueAt,
		ConversationID: request.ConversationID, ContactID: request.ContactID,
		DeleteMemories: request.DeleteMemories,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "task_updated", Outcome: "success",
		EntityType: "task", EntityID: task.ID,
	})
	writeJSON(w, http.StatusOK, task)
}

func (h *handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deleteMemories := r.URL.Query().Get("delete_memories") == "true"
	result, err := h.store.DeleteTask(r.Context(), id, deleteMemories)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "task_deleted", Outcome: "success",
		EntityType: "task", EntityID: id,
	})
	writeJSON(w, http.StatusOK, result)
}

type attachTaskMemoryRequest struct {
	MemoryID string `json:"memory_id"`
}

func (h *handler) attachTaskMemory(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var request attachTaskMemoryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	task, err := h.store.AttachTaskMemory(r.Context(), taskID, request.MemoryID)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "task_memory_attached", Outcome: "success",
		EntityType: "task", EntityID: task.ID,
	})
	writeJSON(w, http.StatusOK, task)
}

func (h *handler) detachTaskMemory(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	memoryID := r.PathValue("memory_id")
	task, err := h.store.DetachTaskMemory(r.Context(), taskID, memoryID)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "task_memory_detached", Outcome: "success",
		EntityType: "task", EntityID: task.ID,
	})
	writeJSON(w, http.StatusOK, task)
}

func (h *handler) listMemories(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memories unavailable")
		return
	}
	query := r.URL.Query()
	page, err := h.memories.List(r.Context(), memories.ListParams{
		ConversationID: query.Get("conversation_id"),
		ContactID:      query.Get("contact_id"),
		Source:         query.Get("source"),
		Query:          query.Get("q"),
		Limit:          parseLimit(r),
		Cursor:         query.Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[memories.Memory]{Data: page.Memories, NextCursor: page.NextCursor})
}

func (h *handler) getMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memories unavailable")
		return
	}
	memory, err := h.memories.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, memory)
}

type createMemoryRequest struct {
	Title          string `json:"title"`
	Content        string `json:"content"`
	Source         string `json:"source"`
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	ContactID      string `json:"contact_id"`
}

func (h *handler) createMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memories unavailable")
		return
	}
	var request createMemoryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	memory, err := h.memories.Create(r.Context(), memories.CreateParams{
		Title: request.Title, Content: request.Content, Source: request.Source,
		MessageID: request.MessageID, ConversationID: request.ConversationID, ContactID: request.ContactID,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "memory_created", Outcome: "success",
		EntityType: "memory", EntityID: memory.ID,
	})
	writeJSON(w, http.StatusOK, memory)
}

type updateMemoryRequest struct {
	Title          *string `json:"title"`
	Content        *string `json:"content"`
	ConversationID *string `json:"conversation_id"`
	ContactID      *string `json:"contact_id"`
}

func (h *handler) updateMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memories unavailable")
		return
	}
	id := r.PathValue("id")
	var request updateMemoryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	memory, err := h.memories.Update(r.Context(), id, memories.UpdateParams{
		Title: request.Title, Content: request.Content,
		ConversationID: request.ConversationID, ContactID: request.ContactID,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "memory_updated", Outcome: "success",
		EntityType: "memory", EntityID: memory.ID,
	})
	writeJSON(w, http.StatusOK, memory)
}

func (h *handler) deleteMemory(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memories unavailable")
		return
	}
	id := r.PathValue("id")
	if err := h.memories.Delete(r.Context(), id); err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "memory_deleted", Outcome: "success",
		EntityType: "memory", EntityID: id,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

type searchMemoriesRequest struct {
	Query          string `json:"query"`
	ConversationID string `json:"conversation_id"`
	ContactID      string `json:"contact_id"`
	Limit          int    `json:"limit"`
}

func (h *handler) searchMemories(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memories unavailable")
		return
	}
	var request searchMemoriesRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.memories.Search(r.Context(), memories.SearchParams{
		Query: request.Query, ConversationID: request.ConversationID,
		ContactID: request.ContactID, Limit: request.Limit,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func parseOptionalDueAt(value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	return parseDateTime(trimmed, false)
}

type uazapiStatusResponse struct {
	Instance struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		ProfileName string `json:"profile_name"`
	} `json:"instance"`
	Connection struct {
		Connected bool `json:"connected"`
		LoggedIn  bool `json:"logged_in"`
	} `json:"connection"`
	Webhook struct {
		Configured bool     `json:"configured"`
		Enabled    bool     `json:"enabled"`
		Events     []string `json:"events"`
	} `json:"webhook"`
	CheckedAt time.Time `json:"checked_at"`
}

func (h *handler) uazapiStatus(w http.ResponseWriter, r *http.Request) {
	if h.uazapi == nil {
		writeError(w, http.StatusServiceUnavailable, "UAZAPI unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, err := h.uazapi.Status(ctx)
	if err != nil {
		h.logger.Warn("UAZAPI status failed", "error", err)
		writeError(w, http.StatusBadGateway, "failed to query UAZAPI")
		return
	}
	webhooks, err := h.uazapi.Webhooks(ctx)
	if err != nil {
		h.logger.Warn("UAZAPI webhook query failed", "error", err)
		writeError(w, http.StatusBadGateway, "failed to query UAZAPI webhook")
		return
	}
	response := uazapiStatusResponse{CheckedAt: time.Now().UTC()}
	response.Instance.ID = status.Instance.ID
	response.Instance.Name = status.Instance.Name
	response.Instance.Status = status.Instance.Status
	response.Instance.ProfileName = status.Instance.ProfileName
	response.Connection.Connected = status.Status.Connected
	response.Connection.LoggedIn = status.Status.LoggedIn
	callbackURL := h.callbackURL()
	for _, webhook := range webhooks {
		if callbackURL != "" && webhook.URL == callbackURL {
			response.Webhook.Configured = true
			response.Webhook.Enabled = webhook.Enabled
			response.Webhook.Events = webhook.Events
			break
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *handler) configureUAZAPIWebhook(w http.ResponseWriter, r *http.Request) {
	callbackURL := h.callbackURL()
	if callbackURL == "" || h.uazapi == nil {
		writeError(w, http.StatusConflict, "UAZAPI webhook public URL is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := h.uazapi.ConfigureWebhook(ctx, callbackURL); err != nil {
		h.logger.Error("UAZAPI webhook configuration failed", "error", err)
		h.recordActivity(r.Context(), activity.Record{Category: "uazapi", Level: "error", Operation: "uazapi_webhook_configured", Outcome: "failed"})
		writeError(w, http.StatusBadGateway, "failed to configure UAZAPI webhook")
		return
	}
	h.recordActivity(r.Context(), activity.Record{Category: "uazapi", Level: "info", Operation: "uazapi_webhook_configured", Outcome: "success"})
	writeJSON(w, http.StatusOK, map[string]bool{"configured": true})
}

func (h *handler) mcpStatus(w http.ResponseWriter, r *http.Request) {
	lastAccess, err := h.store.LastActivityAt(r.Context(), "mcp")
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, admin.MCPStatus{
		Enabled: h.mcpEnabled, Endpoint: "/mcp", Authentication: "bearer",
		Tools:        []string{"search_messages", "list_unread_messages", "acknowledge_messages", "find_conversations", "get_conversation", "get_messages", "get_messages_around", "list_contacts", "get_contact", "list_denylist", "add_to_denylist", "remove_from_denylist", "list_tasks", "get_task", "create_task", "update_task", "delete_task", "attach_memory_to_task", "detach_memory_from_task", "list_task_schedules", "get_task_schedule", "create_task_schedule", "update_task_schedule", "delete_task_schedule", "search_memories", "list_memories", "get_memory", "save_memory", "update_memory", "delete_memory"},
		LastAccessAt: lastAccess,
	})
}

func (h *handler) listActivity(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	page, err := h.store.ListActivity(r.Context(), activity.ListParams{
		Category: r.URL.Query().Get("category"), Level: r.URL.Query().Get("level"),
		From: from, To: to, Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[activity.Event]{Data: page.Events, NextCursor: page.NextCursor})
}

func (h *handler) callbackURL() string {
	if h.webhookPublicURL == "" || h.webhookSecret == "" {
		return ""
	}
	return strings.TrimRight(h.webhookPublicURL, "/") + "/" + h.webhookSecret
}

func (h *handler) recordActivity(ctx context.Context, record activity.Record) {
	if h.store == nil {
		return
	}
	if err := h.store.RecordActivity(ctx, record); err != nil {
		h.logger.Error("activity recording failed", "category", record.Category, "operation", record.Operation, "error", err)
	}
}

func (h *handler) handleStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrNotFound), errors.Is(err, email.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, email.ErrMessageNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, email.ErrDisabled), errors.Is(err, postgres.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, email.ErrProvider):
		writeError(w, http.StatusBadGateway, err.Error())
	case errors.Is(err, email.ErrMissingKey):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, postgres.ErrInvalidArgument), errors.Is(err, email.ErrInvalidArgument),
		errors.Is(err, pagination.ErrInvalidCursor),
		errors.Is(err, memories.ErrInvalidContent), errors.Is(err, memories.ErrEmbedderUnavailable),
		strings.Contains(err.Error(), "direction must"):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.logger.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

type pageResponse[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func parseRange(r *http.Request) (*time.Time, *time.Time, error) {
	from, err := parseDateTime(r.URL.Query().Get("from"), false)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid from: %w", err)
	}
	to, err := parseDateTime(r.URL.Query().Get("to"), true)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid to: %w", err)
	}
	return from, to, nil
}

func parseDateTime(value string, endOfDate bool) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		parsed = parsed.UTC()
		return &parsed, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, errors.New("expected RFC3339 or YYYY-MM-DD")
	}
	if endOfDate {
		parsed = parsed.AddDate(0, 0, 1)
	}
	return &parsed, nil
}

func parseLimit(r *http.Request) int {
	return parseIntDefault(r.URL.Query().Get("limit"), 50)
}

func parseIntDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
