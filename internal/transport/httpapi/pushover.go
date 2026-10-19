package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/pushover"
)

func (h *handler) pushoverStatus(w http.ResponseWriter, r *http.Request) {
	if h.pushover == nil {
		writeError(w, http.StatusServiceUnavailable, "pushover service unavailable")
		return
	}
	settings, err := h.pushover.GetSettings(r.Context())
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (h *handler) pushoverSounds(w http.ResponseWriter, r *http.Request) {
	if h.pushover == nil {
		writeError(w, http.StatusServiceUnavailable, "pushover service unavailable")
		return
	}
	sounds, err := h.pushover.Sounds(r.Context())
	if errors.Is(err, pushover.ErrNotConfigured) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]pushover.Sound{"sounds": sounds})
}

type configurePushoverRequest struct {
	AppToken *string `json:"app_token"`
	UserKey  *string `json:"user_key"`
	Enabled  *bool   `json:"enabled"`
}

func (h *handler) configurePushover(w http.ResponseWriter, r *http.Request) {
	if h.pushover == nil {
		writeError(w, http.StatusServiceUnavailable, "pushover service unavailable")
		return
	}
	var request configurePushoverRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	settings, err := h.pushover.Configure(r.Context(), pushover.ConfigureParams{
		AppToken: request.AppToken, UserKey: request.UserKey, Enabled: request.Enabled,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{Category: "admin", Level: "info", Operation: "pushover_configured", Outcome: "success", EntityType: "integration", EntityID: "pushover"})
	writeJSON(w, http.StatusOK, settings)
}

type sendPushoverRequest struct {
	Message  string `json:"message"`
	Title    string `json:"title"`
	Priority *int   `json:"priority"`
	URL      string `json:"url"`
	Sound    string `json:"sound"`
	Retry    *int   `json:"retry"`
	Expire   *int   `json:"expire"`
}

func (h *handler) sendPushover(w http.ResponseWriter, r *http.Request) {
	if h.pushover == nil {
		writeError(w, http.StatusServiceUnavailable, "pushover service unavailable")
		return
	}
	var request sendPushoverRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.pushover.Send(r.Context(), pushover.SendParams{
		Message: request.Message, Title: request.Title, Priority: request.Priority, URL: request.URL,
		Sound: request.Sound, Retry: request.Retry, Expire: request.Expire,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{Category: "admin", Level: "info", Operation: "pushover_notification_sent", Outcome: "success", EntityType: "integration", EntityID: "pushover"})
	writeJSON(w, http.StatusOK, result)
}
