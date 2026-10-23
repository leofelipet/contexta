package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/auth"
	"github.com/leofelipet/contexta/internal/trello"
)

func (h *handler) trelloWebhook(w http.ResponseWriter, r *http.Request) {
	if h.trello == nil || h.trelloWebhookSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "Trello integration unavailable")
		return
	}
	if !auth.SecureEqual(r.PathValue("secret"), h.trelloWebhookSecret) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	result, err := h.trello.HandleWebhook(r.Context(), body, r.Header.Get("X-Trello-Webhook"))
	if err != nil {
		h.logger.Warn("Trello webhook failed", "error", err)
		h.recordActivity(r.Context(), activity.Record{Category: "webhook", Level: "warning", Operation: "trello_webhook", Outcome: "failed"})
		switch {
		case errors.Is(err, trello.ErrInvalidSignature):
			writeError(w, http.StatusUnauthorized, "unauthorized")
		case errors.Is(err, trello.ErrInvalidArgument):
			writeError(w, http.StatusBadRequest, "invalid webhook payload")
		case errors.Is(err, trello.ErrNotConfigured), errors.Is(err, trello.ErrDisabled):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, trello.ErrProvider):
			writeError(w, http.StatusBadGateway, err.Error())
		case errors.Is(err, trello.ErrMissingKey):
			writeError(w, http.StatusServiceUnavailable, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "webhook processing failed")
		}
		return
	}
	record := activity.Record{Category: "webhook", Level: "info", Operation: "trello_webhook", Outcome: "success"}
	if result.Task.ID != "" {
		record.EntityType, record.EntityID = "task", result.Task.ID
	}
	h.recordActivity(r.Context(), record)
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) trelloStatus(w http.ResponseWriter, r *http.Request) {
	if h.trello == nil {
		writeError(w, http.StatusServiceUnavailable, "Trello integration unavailable")
		return
	}
	settings, err := h.trello.GetSettings(r.Context())
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

type configureTrelloRequest struct {
	APIKey            *string            `json:"api_key"`
	ApplicationSecret *string            `json:"application_secret"`
	Token             *string            `json:"token"`
	BoardID           *string            `json:"board_id"`
	ListStatuses      *map[string]string `json:"list_statuses"`
	Enabled           *bool              `json:"enabled"`
}

func (h *handler) configureTrello(w http.ResponseWriter, r *http.Request) {
	if h.trello == nil {
		writeError(w, http.StatusServiceUnavailable, "Trello integration unavailable")
		return
	}
	var request configureTrelloRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	settings, err := h.trello.Configure(r.Context(), trello.ConfigureParams{
		APIKey: request.APIKey, ApplicationSecret: request.ApplicationSecret, Token: request.Token,
		BoardID: request.BoardID, ListStatuses: request.ListStatuses, Enabled: request.Enabled,
	})
	if err != nil {
		switch {
		case errors.Is(err, trello.ErrWebhookUnavailable):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, trello.ErrInvalidArgument):
			writeError(w, http.StatusBadRequest, "invalid Trello configuration")
		case errors.Is(err, trello.ErrProvider):
			writeError(w, http.StatusBadGateway, err.Error())
		case errors.Is(err, trello.ErrMissingKey):
			writeError(w, http.StatusServiceUnavailable, err.Error())
		default:
			h.handleStoreError(w, err)
		}
		return
	}
	h.recordActivity(r.Context(), activity.Record{Category: "admin", Level: "info", Operation: "trello_configured", Outcome: "success", EntityType: "integration", EntityID: "trello"})
	writeJSON(w, http.StatusOK, settings)
}
