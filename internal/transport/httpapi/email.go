package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/email"
)

type createEmailAccountRequest struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Username string `json:"username"`
	Password string `json:"password"`
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	IMAPTLS  *bool  `json:"imap_use_tls"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	SMTPTLS  *bool  `json:"smtp_use_tls"`
	SaveSent *bool  `json:"save_sent_copy"`
	Enabled  *bool  `json:"enabled"`
}

type updateEmailAccountRequest struct {
	Name     *string `json:"name"`
	Address  *string `json:"address"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	IMAPHost *string `json:"imap_host"`
	IMAPPort *int    `json:"imap_port"`
	IMAPTLS  *bool   `json:"imap_use_tls"`
	SMTPHost *string `json:"smtp_host"`
	SMTPPort *int    `json:"smtp_port"`
	SMTPTLS  *bool   `json:"smtp_use_tls"`
	SaveSent *bool   `json:"save_sent_copy"`
	Enabled  *bool   `json:"enabled"`
}

func (h *handler) listEmailAccounts(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeError(w, http.StatusServiceUnavailable, "email service unavailable")
		return
	}
	enabledOnly := strings.EqualFold(r.URL.Query().Get("enabled"), "true")
	page, err := h.email.ListAccounts(r.Context(), email.ListParams{
		EnabledOnly: enabledOnly,
		Limit:       parseLimit(r),
		Cursor:      r.URL.Query().Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[email.Account]{Data: page.Accounts, NextCursor: page.NextCursor})
}

func (h *handler) getEmailAccount(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeError(w, http.StatusServiceUnavailable, "email service unavailable")
		return
	}
	acc, err := h.email.GetAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}

func (h *handler) createEmailAccount(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeError(w, http.StatusServiceUnavailable, "email service unavailable")
		return
	}
	var request createEmailAccountRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	imapTLS, smtpTLS, enabled := true, true, true
	if request.IMAPTLS != nil {
		imapTLS = *request.IMAPTLS
	}
	if request.SMTPTLS != nil {
		smtpTLS = *request.SMTPTLS
	}
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	acc, err := h.email.CreateAccount(r.Context(), email.CreateParams{
		Name: request.Name, Address: request.Address, Username: request.Username, Password: request.Password,
		IMAPHost: request.IMAPHost, IMAPPort: request.IMAPPort, IMAPTLS: imapTLS,
		SMTPHost: request.SMTPHost, SMTPPort: request.SMTPPort, SMTPTLS: smtpTLS,
		SaveSentCopy: request.SaveSent, Enabled: enabled,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "email_account_created", Outcome: "success",
		EntityType: "email_account", EntityID: acc.ID,
	})
	writeJSON(w, http.StatusOK, acc)
}

func (h *handler) updateEmailAccount(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeError(w, http.StatusServiceUnavailable, "email service unavailable")
		return
	}
	var request updateEmailAccountRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	acc, err := h.email.UpdateAccount(r.Context(), r.PathValue("id"), email.UpdateParams{
		Name: request.Name, Address: request.Address, Username: request.Username, Password: request.Password,
		IMAPHost: request.IMAPHost, IMAPPort: request.IMAPPort, IMAPTLS: request.IMAPTLS,
		SMTPHost: request.SMTPHost, SMTPPort: request.SMTPPort, SMTPTLS: request.SMTPTLS,
		SaveSentCopy: request.SaveSent, Enabled: request.Enabled,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "email_account_updated", Outcome: "success",
		EntityType: "email_account", EntityID: acc.ID,
	})
	writeJSON(w, http.StatusOK, acc)
}

func (h *handler) deleteEmailAccount(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeError(w, http.StatusServiceUnavailable, "email service unavailable")
		return
	}
	id := r.PathValue("id")
	if err := h.email.DeleteAccount(r.Context(), id); err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "email_account_deleted", Outcome: "success",
		EntityType: "email_account", EntityID: id,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *handler) testEmailAccount(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeError(w, http.StatusServiceUnavailable, "email service unavailable")
		return
	}
	result, err := h.email.TestAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	status := http.StatusOK
	if !result.IMAPOK || !result.SMTPOK {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, result)
}
