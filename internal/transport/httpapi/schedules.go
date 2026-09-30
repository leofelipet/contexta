package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/schedules"
)

type createTaskScheduleRequest struct {
	Cron           string `json:"cron"`
	Timezone       string `json:"timezone"`
	Enabled        *bool  `json:"enabled"`
	SkipIfOpen     bool   `json:"skip_if_open"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	CompanyID      string `json:"company_id"`
	DueInMinutes   *int   `json:"due_in_minutes"`
	ConversationID string `json:"conversation_id"`
	ContactID      string `json:"contact_id"`
}

type updateTaskScheduleRequest struct {
	Cron           *string `json:"cron"`
	Timezone       *string `json:"timezone"`
	Enabled        *bool   `json:"enabled"`
	SkipIfOpen     *bool   `json:"skip_if_open"`
	Title          *string `json:"title"`
	Description    *string `json:"description"`
	CompanyID      *string `json:"company_id"`
	DueInMinutes   *int    `json:"due_in_minutes"`
	ConversationID *string `json:"conversation_id"`
	ContactID      *string `json:"contact_id"`
}

func (h *handler) listTaskSchedules(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var enabled *bool
	if value := query.Get("enabled"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid enabled; expected true or false")
			return
		}
		enabled = &parsed
	}
	page, err := h.store.ListTaskSchedules(r.Context(), schedules.ListParams{
		Enabled: enabled, CompanyID: query.Get("company_id"), Query: query.Get("q"), Limit: parseLimit(r), Cursor: query.Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[schedules.Schedule]{Data: page.Schedules, NextCursor: page.NextCursor})
}

func (h *handler) getTaskSchedule(w http.ResponseWriter, r *http.Request) {
	schedule, err := h.store.GetTaskSchedule(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schedule)
}

func (h *handler) createTaskSchedule(w http.ResponseWriter, r *http.Request) {
	var request createTaskScheduleRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	schedule, err := h.store.CreateTaskSchedule(r.Context(), schedules.CreateParams{
		Cron: request.Cron, Timezone: request.Timezone, Enabled: request.Enabled, SkipIfOpen: request.SkipIfOpen,
		Title: request.Title, Description: request.Description, CompanyID: request.CompanyID,
		DueInMinutes: request.DueInMinutes, ConversationID: request.ConversationID, ContactID: request.ContactID,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "task_schedule_created", Outcome: "success",
		EntityType: "task_schedule", EntityID: schedule.ID,
	})
	writeJSON(w, http.StatusOK, schedule)
}

func (h *handler) updateTaskSchedule(w http.ResponseWriter, r *http.Request) {
	var request updateTaskScheduleRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	schedule, err := h.store.UpdateTaskSchedule(r.Context(), r.PathValue("id"), schedules.UpdateParams{
		Cron: request.Cron, Timezone: request.Timezone, Enabled: request.Enabled, SkipIfOpen: request.SkipIfOpen,
		Title: request.Title, Description: request.Description, CompanyID: request.CompanyID,
		DueInMinutes: request.DueInMinutes, ConversationID: request.ConversationID, ContactID: request.ContactID,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "task_schedule_updated", Outcome: "success",
		EntityType: "task_schedule", EntityID: schedule.ID,
	})
	writeJSON(w, http.StatusOK, schedule)
}

func (h *handler) deleteTaskSchedule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteTaskSchedule(r.Context(), id); err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "task_schedule_deleted", Outcome: "success",
		EntityType: "task_schedule", EntityID: id,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
