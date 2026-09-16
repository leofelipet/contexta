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

	"github.com/leofelipe/contexta/internal/auth"
	"github.com/leofelipe/contexta/internal/contacts"
	"github.com/leofelipe/contexta/internal/conversations"
	"github.com/leofelipe/contexta/internal/ingestion"
	"github.com/leofelipe/contexta/internal/messages"
	"github.com/leofelipe/contexta/internal/pagination"
	"github.com/leofelipe/contexta/internal/providers/whatsapp/uazapi"
	"github.com/leofelipe/contexta/internal/storage/postgres"
)

const maxWebhookBody = 2 << 20

type Store interface {
	Ping(context.Context) error
	ListContacts(context.Context, contacts.ListParams) (contacts.Page, error)
	GetContact(context.Context, string) (contacts.Contact, error)
	ListConversations(context.Context, conversations.ListParams) (conversations.Page, error)
	GetConversation(context.Context, string) (conversations.Conversation, error)
	SearchMessages(context.Context, messages.SearchParams) (messages.Page, error)
	GetMessage(context.Context, string) (messages.Message, error)
	GetMessagesAround(context.Context, string, int, int) (messages.Around, error)
}

type Options struct {
	Store              Store
	Ingestion          *ingestion.Service
	APIToken           string
	WebhookSecret      string
	ProviderInstanceID string
	CaptureDir         string
	Logger             *slog.Logger
}

func New(options Options) http.Handler {
	handler := &handler{
		store:              options.Store,
		ingestion:          options.Ingestion,
		webhookSecret:      options.WebhookSecret,
		providerInstanceID: options.ProviderInstanceID,
		captureDir:         options.CaptureDir,
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
	api.HandleFunc("GET /api/v1/conversations/{id}", handler.getConversation)
	api.HandleFunc("GET /api/v1/conversations/{id}/messages", handler.conversationMessages)
	api.HandleFunc("GET /api/v1/messages", handler.searchMessages)
	api.HandleFunc("GET /api/v1/messages/{id}", handler.getMessage)
	api.HandleFunc("GET /api/v1/messages/{id}/around", handler.messagesAround)
	root.Handle("/api/v1/", auth.NewMiddleware(options.APIToken).Wrap(api))

	return accessLog(options.Logger, recoverPanic(options.Logger, root))
}

type handler struct {
	store              Store
	ingestion          *ingestion.Service
	webhookSecret      string
	providerInstanceID string
	captureDir         string
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
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		h.logger.Warn("webhook validation failed", "provider", "uazapi", "error", err)
		writeError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	if len(event.Messages) == 0 {
		h.logger.Info("webhook event processed", "provider", "uazapi", "event_type", event.Type, "messages", 0)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	result, err := h.ingestion.Ingest(r.Context(), ingestion.Batch{
		Provider: "uazapi", ProviderInstanceID: h.providerInstanceID, Messages: event.Messages,
	})
	if err != nil {
		h.logger.Error("webhook processing failed", "provider", "uazapi", "event_type", event.Type, "error", err)
		writeError(w, http.StatusInternalServerError, "webhook processing failed")
		return
	}
	h.logger.Info("webhook event processed", "provider", "uazapi", "event_type", event.Type,
		"processed", result.Processed, "created", result.Created, "updated", result.Updated)
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listContacts(w http.ResponseWriter, r *http.Request) {
	page, err := h.store.ListContacts(r.Context(), contacts.ListParams{
		Query: r.URL.Query().Get("query"), Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
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
		From: from, To: to, Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[conversations.Conversation]{Data: page.Conversations, NextCursor: page.NextCursor})
}

func (h *handler) getConversation(w http.ResponseWriter, r *http.Request) {
	conversation, err := h.store.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversation)
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
		Direction: r.URL.Query().Get("direction"), Limit: parseLimit(r), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[messages.Message]{Data: page.Messages, NextCursor: page.NextCursor})
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

func (h *handler) handleStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, postgres.ErrInvalidArgument), errors.Is(err, pagination.ErrInvalidCursor), strings.Contains(err.Error(), "direction must"):
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
