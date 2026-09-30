package mcpserver

import (
	"context"
	"errors"

	"github.com/leofelipet/contexta/internal/companies"
	"github.com/leofelipet/contexta/internal/storage/postgres"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *server) addCompanyTools(mcpServer *mcp.Server) {
	mcp.AddTool(mcpServer, readOnlyTool("list_companies", "List companies sorted by name, with open/total task counts and linked contact counts. Optional text query matches name, notes, or numeric ID."), s.listCompanies)
	mcp.AddTool(mcpServer, readOnlyTool("get_company", "Get one company by numeric ID. Use list_tasks with company_id and list_contacts with company_id to see what is linked to it."), s.getCompany)
	mcp.AddTool(mcpServer, localWriteTool("create_company", "Create a company with a unique name (case-insensitive) and optional notes. Link tasks via create_task/update_task company_id and contacts via attach_contact_to_company."), s.createCompany)
	mcp.AddTool(mcpServer, localWriteTool("update_company", "Update a company's name or notes."), s.updateCompany)
	mcp.AddTool(mcpServer, localWriteTool("delete_company", "Permanently delete a company. Linked tasks, recurring schedules, and contacts are kept without a company."), s.deleteCompany)
	mcp.AddTool(mcpServer, localWriteTool("attach_contact_to_company", "Link a WhatsApp contact to a company, replacing its previous company. New tasks linked to this contact without an explicit company_id inherit this company."), s.attachContactToCompany)
	mcp.AddTool(mcpServer, localWriteTool("detach_contact_from_company", "Remove the link between a WhatsApp contact and a company without deleting either."), s.detachContactFromCompany)
}

type listCompaniesInput struct {
	Query  string `json:"query,omitempty" jsonschema:"Text matched against name and notes, or an exact numeric company ID."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of companies, up to 100."`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque cursor returned by the previous call."`
}

type companiesOutput struct {
	Companies  []companies.Company `json:"companies"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func (s *server) listCompanies(ctx context.Context, _ *mcp.CallToolRequest, input listCompaniesInput) (*mcp.CallToolResult, companiesOutput, error) {
	s.logAccess(ctx, "list_companies")
	page, err := s.store.ListCompanies(ctx, companies.ListParams{Query: input.Query, Limit: mcpLimit(input.Limit), Cursor: input.Cursor})
	if err != nil {
		s.logError(ctx, "list_companies", err)
		return nil, companiesOutput{}, safeCompanyToolError(err)
	}
	return nil, companiesOutput{Companies: page.Companies, NextCursor: page.NextCursor}, nil
}

type companyIDInput struct {
	ID string `json:"id" jsonschema:"Required numeric company ID (e.g. 1, 2, 3)."`
}

type companyOutput struct {
	Company companies.Company `json:"company"`
}

func (s *server) getCompany(ctx context.Context, _ *mcp.CallToolRequest, input companyIDInput) (*mcp.CallToolResult, companyOutput, error) {
	s.logAccess(ctx, "get_company")
	company, err := s.store.GetCompany(ctx, input.ID)
	if err != nil {
		s.logError(ctx, "get_company", err)
		return nil, companyOutput{}, safeCompanyToolError(err)
	}
	return nil, companyOutput{Company: company}, nil
}

type createCompanyInput struct {
	Name  string `json:"name" jsonschema:"Required company name, unique ignoring case (max 200 characters)."`
	Notes string `json:"notes,omitempty" jsonschema:"Optional free-text notes about the company."`
}

func (s *server) createCompany(ctx context.Context, _ *mcp.CallToolRequest, input createCompanyInput) (*mcp.CallToolResult, companyOutput, error) {
	s.logAccess(ctx, "create_company")
	company, err := s.store.CreateCompany(ctx, companies.CreateParams{Name: input.Name, Notes: input.Notes})
	if err != nil {
		s.logError(ctx, "create_company", err)
		return nil, companyOutput{}, safeCompanyToolError(err)
	}
	return nil, companyOutput{Company: company}, nil
}

type updateCompanyInput struct {
	ID    string  `json:"id" jsonschema:"Required numeric company ID (e.g. 1, 2, 3)."`
	Name  *string `json:"name,omitempty" jsonschema:"New company name."`
	Notes *string `json:"notes,omitempty" jsonschema:"New notes. Empty string clears them."`
}

func (s *server) updateCompany(ctx context.Context, _ *mcp.CallToolRequest, input updateCompanyInput) (*mcp.CallToolResult, companyOutput, error) {
	s.logAccess(ctx, "update_company")
	company, err := s.store.UpdateCompany(ctx, input.ID, companies.UpdateParams{Name: input.Name, Notes: input.Notes})
	if err != nil {
		s.logError(ctx, "update_company", err)
		return nil, companyOutput{}, safeCompanyToolError(err)
	}
	return nil, companyOutput{Company: company}, nil
}

type deleteCompanyOutput struct {
	Deleted bool `json:"deleted"`
}

func (s *server) deleteCompany(ctx context.Context, _ *mcp.CallToolRequest, input companyIDInput) (*mcp.CallToolResult, deleteCompanyOutput, error) {
	s.logAccess(ctx, "delete_company")
	if err := s.store.DeleteCompany(ctx, input.ID); err != nil {
		s.logError(ctx, "delete_company", err)
		return nil, deleteCompanyOutput{}, safeCompanyToolError(err)
	}
	return nil, deleteCompanyOutput{Deleted: true}, nil
}

type companyContactInput struct {
	CompanyID string `json:"company_id" jsonschema:"Required numeric company ID."`
	ContactID string `json:"contact_id" jsonschema:"Required Contexta contact UUID."`
}

func (s *server) attachContactToCompany(ctx context.Context, _ *mcp.CallToolRequest, input companyContactInput) (*mcp.CallToolResult, companyOutput, error) {
	s.logAccess(ctx, "attach_contact_to_company")
	company, err := s.store.AttachContactToCompany(ctx, input.CompanyID, input.ContactID)
	if err != nil {
		s.logError(ctx, "attach_contact_to_company", err)
		return nil, companyOutput{}, safeCompanyToolError(err)
	}
	return nil, companyOutput{Company: company}, nil
}

func (s *server) detachContactFromCompany(ctx context.Context, _ *mcp.CallToolRequest, input companyContactInput) (*mcp.CallToolResult, companyOutput, error) {
	s.logAccess(ctx, "detach_contact_from_company")
	company, err := s.store.DetachContactFromCompany(ctx, input.CompanyID, input.ContactID)
	if err != nil {
		s.logError(ctx, "detach_contact_from_company", err)
		return nil, companyOutput{}, safeCompanyToolError(err)
	}
	return nil, companyOutput{Company: company}, nil
}

// safeCompanyToolError tells the agent about duplicate names so it can reuse
// the existing company; everything else goes through the generic mapping.
func safeCompanyToolError(err error) error {
	if errors.Is(err, postgres.ErrConflict) {
		return errors.New("a company with this name already exists; use list_companies to find it")
	}
	return safeToolError(err)
}
