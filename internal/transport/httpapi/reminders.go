package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/schedules"
)

// send_at accepts RFC3339 or a local date-time interpreted in timezone
// (America/Sao_Paulo when empty), so the admin UI can post datetime-local values.
type createReminderRequest struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Priority *int   `json:"priority"`
	Sound    string `json:"sound"`
	SendAt   string `json:"send_at"`
	Timezone string `json:"timezone"`
}

type updateReminderRequest struct {
	Title    *string `json:"title"`
	Message  *string `json:"message"`
	Priority *int    `json:"priority"`
	Sound    *string `json:"sound"`
	SendAt   *string `json:"send_at"`
	Timezone string  `json:"timezone"`
}

func (h *handler) listReminders(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, err := h.store.ListReminders(r.Context(), schedules.ReminderListParams{
		Status: query.Get("status"), Limit: parseLimit(r), Cursor: query.Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[schedules.Reminder]{Data: page.Reminders, NextCursor: page.NextCursor})
}

func (h *handler) getReminder(w http.ResponseWriter, r *http.Request) {
	reminder, err := h.store.GetReminder(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reminder)
}

func (h *handler) createReminder(w http.ResponseWriter, r *http.Request) {
	var request createReminderRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sendAt, err := schedules.ParseSendAt(request.SendAt, request.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reminder, err := h.store.CreateReminder(r.Context(), schedules.ReminderCreateParams{
		Title: request.Title, Message: request.Message, Priority: request.Priority, Sound: request.Sound, SendAt: sendAt,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "reminder_created", Outcome: "success",
		EntityType: "reminder", EntityID: reminder.ID,
	})
	writeJSON(w, http.StatusOK, reminder)
}

func (h *handler) updateReminder(w http.ResponseWriter, r *http.Request) {
	var request updateReminderRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var sendAt *time.Time
	if request.SendAt != nil {
		parsed, err := schedules.ParseSendAt(*request.SendAt, request.Timezone)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		sendAt = &parsed
	}
	reminder, err := h.store.UpdateReminder(r.Context(), r.PathValue("id"), schedules.ReminderUpdateParams{
		Title: request.Title, Message: request.Message, Priority: request.Priority, Sound: request.Sound, SendAt: sendAt,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "reminder_updated", Outcome: "success",
		EntityType: "reminder", EntityID: reminder.ID,
	})
	writeJSON(w, http.StatusOK, reminder)
}

func (h *handler) deleteReminder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteReminder(r.Context(), id); err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "reminder_deleted", Outcome: "success",
		EntityType: "reminder", EntityID: id,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
