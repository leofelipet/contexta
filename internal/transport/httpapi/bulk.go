package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/leofelipet/contexta/internal/activity"
)

type bulkIDsRequest struct {
	IDs []string `json:"ids"`
}

type bulkTasksDeleteRequest struct {
	IDs            []string `json:"ids"`
	DeleteMemories bool     `json:"delete_memories"`
}

type bulkTasksUpdateRequest struct {
	IDs            []string `json:"ids"`
	Status         string   `json:"status"`
	DeleteMemories bool     `json:"delete_memories"`
}

type bulkTaskSchedulesUpdateRequest struct {
	IDs     []string `json:"ids"`
	Enabled *bool    `json:"enabled"`
}

func decodeBulkRequest(w http.ResponseWriter, r *http.Request, request any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func (h *handler) recordBulkActivity(r *http.Request, operation, entityType string, count int) {
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: operation, Outcome: "success",
		EntityType: entityType, Metadata: json.RawMessage(fmt.Sprintf(`{"count":%d}`, count)),
	})
}

func (h *handler) bulkDeleteTasks(w http.ResponseWriter, r *http.Request) {
	var request bulkTasksDeleteRequest
	if !decodeBulkRequest(w, r, &request) {
		return
	}
	result, err := h.store.DeleteTasks(r.Context(), request.IDs, request.DeleteMemories)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordBulkActivity(r, "tasks_bulk_deleted", "task", result.Count)
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) bulkUpdateTasks(w http.ResponseWriter, r *http.Request) {
	var request bulkTasksUpdateRequest
	if !decodeBulkRequest(w, r, &request) {
		return
	}
	result, err := h.store.UpdateTasksStatus(r.Context(), request.IDs, request.Status, request.DeleteMemories)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordBulkActivity(r, "tasks_bulk_updated", "task", result.Count)
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) bulkDeleteMemories(w http.ResponseWriter, r *http.Request) {
	if h.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "memories unavailable")
		return
	}
	var request bulkIDsRequest
	if !decodeBulkRequest(w, r, &request) {
		return
	}
	count, err := h.memories.DeleteMany(r.Context(), request.IDs)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordBulkActivity(r, "memories_bulk_deleted", "memory", count)
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

func (h *handler) bulkDeleteTaskSchedules(w http.ResponseWriter, r *http.Request) {
	var request bulkIDsRequest
	if !decodeBulkRequest(w, r, &request) {
		return
	}
	count, err := h.store.DeleteTaskSchedules(r.Context(), request.IDs)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordBulkActivity(r, "task_schedules_bulk_deleted", "task_schedule", count)
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

func (h *handler) bulkUpdateTaskSchedules(w http.ResponseWriter, r *http.Request) {
	var request bulkTaskSchedulesUpdateRequest
	if !decodeBulkRequest(w, r, &request) {
		return
	}
	if request.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	count, err := h.store.SetTaskSchedulesEnabled(r.Context(), request.IDs, *request.Enabled)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordBulkActivity(r, "task_schedules_bulk_updated", "task_schedule", count)
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

func (h *handler) bulkDeleteCompanies(w http.ResponseWriter, r *http.Request) {
	var request bulkIDsRequest
	if !decodeBulkRequest(w, r, &request) {
		return
	}
	count, err := h.store.DeleteCompanies(r.Context(), request.IDs)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordBulkActivity(r, "companies_bulk_deleted", "company", count)
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

func (h *handler) bulkRemoveDenylistEntries(w http.ResponseWriter, r *http.Request) {
	var request bulkIDsRequest
	if !decodeBulkRequest(w, r, &request) {
		return
	}
	count, err := h.store.RemoveDenylistEntries(r.Context(), request.IDs)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordBulkActivity(r, "denylist_bulk_removed", "denylist", count)
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}
