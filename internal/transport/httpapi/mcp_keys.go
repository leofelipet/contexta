package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/mcpkeys"
)

type mcpKeyRequest struct {
	Name      string     `json:"name"`
	Access    string     `json:"access"`
	Tools     []string   `json:"tools"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type createdMCPKey struct {
	Key   mcpkeys.Key `json:"key"`
	Token string      `json:"token"`
}

func (h *handler) listMCPKeys(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListMCPKeys(r.Context())
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[mcpkeys.Key]{Data: items})
}

func (h *handler) createMCPKey(w http.ResponseWriter, r *http.Request) {
	request, ok := h.decodeMCPKey(w, r)
	if !ok {
		return
	}
	token, prefix, hash, err := mcpkeys.Generate()
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	key, err := h.store.CreateMCPKey(r.Context(), mcpkeys.Params{
		Name: request.Name, Access: request.Access, Tools: request.Tools, ExpiresAt: request.ExpiresAt,
	}, prefix, hash)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "mcp_key_created", Outcome: "success",
		EntityType: "mcp_key", EntityID: key.ID,
	})
	writeJSON(w, http.StatusOK, createdMCPKey{Key: key, Token: token})
}

func (h *handler) updateMCPKey(w http.ResponseWriter, r *http.Request) {
	request, ok := h.decodeMCPKey(w, r)
	if !ok {
		return
	}
	key, err := h.store.UpdateMCPKey(r.Context(), r.PathValue("id"), mcpkeys.Params{
		Name: request.Name, Access: request.Access, Tools: request.Tools,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "mcp_key_updated", Outcome: "success",
		EntityType: "mcp_key", EntityID: key.ID,
	})
	writeJSON(w, http.StatusOK, key)
}

func (h *handler) deleteMCPKey(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteMCPKey(r.Context(), r.PathValue("id")); err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "mcp_key_deleted", Outcome: "success",
		EntityType: "mcp_key", EntityID: r.PathValue("id"),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *handler) decodeMCPKey(w http.ResponseWriter, r *http.Request) (mcpKeyRequest, bool) {
	var request mcpKeyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return request, false
	}
	if request.Access == mcpkeys.AccessCustom {
		for _, name := range request.Tools {
			if !slices.ContainsFunc(h.mcpTools, func(tool mcpkeys.Tool) bool { return tool.Name == name }) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown tool: %s", name))
				return request, false
			}
		}
	}
	return request, true
}
