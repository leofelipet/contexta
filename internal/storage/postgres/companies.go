package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/leofelipet/contexta/internal/companies"
	"github.com/leofelipet/contexta/internal/pagination"
)

// ErrConflict reports a write rejected by a uniqueness rule, such as a
// company name already in use.
var ErrConflict = errors.New("conflict")

const companySelectCols = `
	co.id::text, co.name, co.notes,
	(SELECT count(*) FROM tasks t WHERE t.company_id = co.id AND t.status NOT IN ('done', 'cancelled')),
	(SELECT count(*) FROM tasks t WHERE t.company_id = co.id),
	(SELECT count(*) FROM contacts ct WHERE ct.company_id = co.id),
	co.created_at, co.updated_at`

func (s *Store) ListCompanies(ctx context.Context, params companies.ListParams) (companies.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "companies")
	if err != nil {
		return companies.Page{}, err
	}
	if params.Cursor != "" && !isTaskID(cursor.ID) {
		return companies.Page{}, pagination.ErrInvalidCursor
	}

	// "#12" is an exact ID lookup; any other text also matches name and notes.
	textQuery := strings.TrimSpace(params.Query)
	var queryID any
	if idPart, ok := strings.CutPrefix(textQuery, "#"); ok && isTaskID(idPart) {
		queryID = nullableTaskID(idPart)
		textQuery = ""
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+companySelectCols+`
		FROM companies co
		WHERE ($1 = '' OR (lower(co.name), co.id) > (lower($1), $2::bigint))
		  AND ($3 = '' OR co.name ILIKE '%' || $3 || '%' OR co.notes ILIKE '%' || $3 || '%' OR co.id::text = $3)
		  AND ($4::bigint IS NULL OR co.id = $4::bigint)
		ORDER BY lower(co.name), co.id
		LIMIT $5`,
		cursor.Text, nullableTaskID(cursor.ID), textQuery, queryID, limit+1,
	)
	if err != nil {
		return companies.Page{}, fmt.Errorf("list companies: %w", err)
	}
	defer rows.Close()

	items := make([]companies.Company, 0, limit+1)
	for rows.Next() {
		company, err := scanCompany(rows)
		if err != nil {
			return companies.Page{}, err
		}
		items = append(items, company)
	}
	if err := rows.Err(); err != nil {
		return companies.Page{}, fmt.Errorf("iterate companies: %w", err)
	}

	page := companies.Page{Companies: items[:min(limit, len(items))]}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "companies", Text: last.Name, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetCompany(ctx context.Context, id string) (companies.Company, error) {
	companyID, err := parseTaskID(id)
	if err != nil {
		return companies.Company{}, err
	}
	row := s.pool.QueryRow(ctx, `SELECT `+companySelectCols+` FROM companies co WHERE co.id = $1`, companyID)
	company, err := scanCompany(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return companies.Company{}, ErrNotFound
	}
	if err != nil {
		return companies.Company{}, fmt.Errorf("get company: %w", err)
	}
	return company, nil
}

func (s *Store) CreateCompany(ctx context.Context, params companies.CreateParams) (companies.Company, error) {
	name, notes, err := validateCompany(params.Name, params.Notes)
	if err != nil {
		return companies.Company{}, err
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO companies (name, notes) VALUES ($1, $2)
		RETURNING id::text`, name, notes,
	).Scan(&id)
	if err != nil {
		return companies.Company{}, companyWriteError("create company", err)
	}
	return s.GetCompany(ctx, id)
}

func (s *Store) UpdateCompany(ctx context.Context, id string, params companies.UpdateParams) (companies.Company, error) {
	companyID, err := parseTaskID(id)
	if err != nil {
		return companies.Company{}, err
	}
	current, err := s.GetCompany(ctx, id)
	if err != nil {
		return companies.Company{}, err
	}
	name, notes := current.Name, current.Notes
	if params.Name != nil {
		name = *params.Name
	}
	if params.Notes != nil {
		notes = *params.Notes
	}
	name, notes, err = validateCompany(name, notes)
	if err != nil {
		return companies.Company{}, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE companies SET name = $2, notes = $3, updated_at = now()
		WHERE id = $1`, companyID, name, notes)
	if err != nil {
		return companies.Company{}, companyWriteError("update company", err)
	}
	if tag.RowsAffected() == 0 {
		return companies.Company{}, ErrNotFound
	}
	return s.GetCompany(ctx, id)
}

// DeleteCompany removes the company; linked tasks, schedules, and contacts are
// kept and lose their company_id.
func (s *Store) DeleteCompany(ctx context.Context, id string) error {
	companyID, err := parseTaskID(id)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM companies WHERE id = $1`, companyID)
	if err != nil {
		return fmt.Errorf("delete company: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AttachContactToCompany sets the contact's company, replacing any previous one.
func (s *Store) AttachContactToCompany(ctx context.Context, companyID, contactID string) (companies.Company, error) {
	id, err := s.requireCompany(ctx, companyID)
	if err != nil {
		return companies.Company{}, err
	}
	if !isUUID(contactID) {
		return companies.Company{}, ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `UPDATE contacts SET company_id = $2 WHERE id = $1::uuid`, contactID, id)
	if err != nil {
		return companies.Company{}, fmt.Errorf("attach contact to company: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return companies.Company{}, ErrNotFound
	}
	return s.GetCompany(ctx, companyID)
}

// DetachContactFromCompany clears the contact's company when it is companyID.
func (s *Store) DetachContactFromCompany(ctx context.Context, companyID, contactID string) (companies.Company, error) {
	id, err := parseTaskID(companyID)
	if err != nil {
		return companies.Company{}, err
	}
	if !isUUID(contactID) {
		return companies.Company{}, ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE contacts SET company_id = NULL
		WHERE id = $1::uuid AND company_id = $2`, contactID, id)
	if err != nil {
		return companies.Company{}, fmt.Errorf("detach contact from company: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return companies.Company{}, ErrNotFound
	}
	return s.GetCompany(ctx, companyID)
}

// requireCompany parses a company ID and checks that it exists.
func (s *Store) requireCompany(ctx context.Context, value string) (int64, error) {
	id, err := parseTaskID(value)
	if err != nil {
		return 0, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM companies WHERE id = $1)`, id).Scan(&exists); err != nil {
		return 0, fmt.Errorf("check companies exists: %w", err)
	}
	if !exists {
		return 0, ErrNotFound
	}
	return id, nil
}

// optionalCompanyID validates an optional company link; "" means no company.
func (s *Store) optionalCompanyID(ctx context.Context, value string) (any, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	id, err := s.requireCompany(ctx, value)
	if err != nil {
		return nil, err
	}
	return id, nil
}

func validateCompany(name, notes string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return "", "", ErrInvalidArgument
	}
	notes = strings.TrimSpace(notes)
	if len(notes) > 10000 {
		return "", "", ErrInvalidArgument
	}
	return name, notes, nil
}

func companyWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: company name already exists", ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func scanCompany(row taskScanner) (companies.Company, error) {
	var company companies.Company
	if err := row.Scan(
		&company.ID, &company.Name, &company.Notes,
		&company.OpenTaskCount, &company.TaskCount, &company.ContactCount,
		&company.CreatedAt, &company.UpdatedAt,
	); err != nil {
		return companies.Company{}, err
	}
	return company, nil
}
