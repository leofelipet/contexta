package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/passkeys"
)

type createAdminPasskeyRequest struct {
	ID         string   `json:"id"`
	PublicKey  string   `json:"public_key"`
	Counter    int64    `json:"counter"`
	Transports []string `json:"transports"`
	DeviceType string   `json:"device_type"`
	BackedUp   bool     `json:"backed_up"`
	Name       string   `json:"name"`
}

type updateAdminPasskeyRequest struct {
	Counter *int64 `json:"counter"`
}

func (h *handler) listAdminPasskeys(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListAdminPasskeys(r.Context())
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[passkeys.Passkey]{Data: items})
}

func (h *handler) createAdminPasskey(w http.ResponseWriter, r *http.Request) {
	var request createAdminPasskeyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	passkey, err := h.store.CreateAdminPasskey(r.Context(), passkeys.CreateParams{
		ID: request.ID, PublicKey: request.PublicKey, Counter: request.Counter,
		Transports: request.Transports, DeviceType: request.DeviceType, BackedUp: request.BackedUp, Name: request.Name,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "passkey_created", Outcome: "success",
		EntityType: "admin_passkey", EntityID: passkey.ID,
	})
	writeJSON(w, http.StatusOK, passkey)
}

func (h *handler) updateAdminPasskey(w http.ResponseWriter, r *http.Request) {
	var request updateAdminPasskeyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Counter == nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	passkey, err := h.store.UpdateAdminPasskeyCounter(r.Context(), r.PathValue("id"), *request.Counter)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, passkey)
}

func (h *handler) deleteAdminPasskey(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteAdminPasskey(r.Context(), r.PathValue("id")); err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "passkey_deleted", Outcome: "success",
		EntityType: "admin_passkey", EntityID: r.PathValue("id"),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
