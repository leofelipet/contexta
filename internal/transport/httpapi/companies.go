package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/leofelipet/contexta/internal/activity"
	"github.com/leofelipet/contexta/internal/companies"
)

type createCompanyRequest struct {
	Name  string `json:"name"`
	Notes string `json:"notes"`
}

type updateCompanyRequest struct {
	Name  *string `json:"name"`
	Notes *string `json:"notes"`
}

type attachCompanyContactRequest struct {
	ContactID string `json:"contact_id"`
}

func (h *handler) listCompanies(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, err := h.store.ListCompanies(r.Context(), companies.ListParams{
		Query: query.Get("q"), Limit: parseLimit(r), Cursor: query.Get("cursor"),
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pageResponse[companies.Company]{Data: page.Companies, NextCursor: page.NextCursor})
}

func (h *handler) getCompany(w http.ResponseWriter, r *http.Request) {
	company, err := h.store.GetCompany(r.Context(), r.PathValue("id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, company)
}

func (h *handler) createCompany(w http.ResponseWriter, r *http.Request) {
	var request createCompanyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	company, err := h.store.CreateCompany(r.Context(), companies.CreateParams{Name: request.Name, Notes: request.Notes})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "company_created", Outcome: "success",
		EntityType: "company", EntityID: company.ID,
	})
	writeJSON(w, http.StatusOK, company)
}

func (h *handler) updateCompany(w http.ResponseWriter, r *http.Request) {
	var request updateCompanyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	company, err := h.store.UpdateCompany(r.Context(), r.PathValue("id"), companies.UpdateParams{
		Name: request.Name, Notes: request.Notes,
	})
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "company_updated", Outcome: "success",
		EntityType: "company", EntityID: company.ID,
	})
	writeJSON(w, http.StatusOK, company)
}

func (h *handler) deleteCompany(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteCompany(r.Context(), id); err != nil {
		h.handleStoreError(w, err)
		return
	}
	h.recordActivity(r.Context(), activity.Record{
		Category: "admin", Level: "info", Operation: "company_deleted", Outcome: "success",
		EntityType: "company", EntityID: id,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *handler) attachCompanyContact(w http.ResponseWriter, r *http.Request) {
	var request attachCompanyContactRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIWriteBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	company, err := h.store.AttachContactToCompany(r.Context(), r.PathValue("id"), request.ContactID)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, company)
}

func (h *handler) detachCompanyContact(w http.ResponseWriter, r *http.Request) {
	company, err := h.store.DetachContactFromCompany(r.Context(), r.PathValue("id"), r.PathValue("contact_id"))
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, company)
}
