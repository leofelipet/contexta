package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/email"
	"github.com/leofelipet/contexta/internal/pagination"
)

const emailAccountSelectCols = `
	id::text, name, address, username, password_ciphertext,
	imap_host, imap_port, imap_use_tls,
	smtp_host, smtp_port, smtp_use_tls,
	save_sent_copy, enabled, created_at, updated_at`

func (s *Store) ListEmailAccounts(ctx context.Context, params email.ListParams) (email.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "email_accounts")
	if err != nil {
		return email.Page{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return email.Page{}, pagination.ErrInvalidCursor
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+emailAccountSelectCols+`
		FROM email_accounts
		WHERE ($1::timestamptz IS NULL OR (created_at, id) < ($1, $2::uuid))
		  AND (NOT $3 OR enabled)
		ORDER BY created_at DESC, id DESC
		LIMIT $4`,
		nullableTime(cursor.Time), nullableUUID(cursor.ID), params.EnabledOnly, limit+1,
	)
	if err != nil {
		return email.Page{}, fmt.Errorf("list email accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]email.Account, 0, limit+1)
	for rows.Next() {
		acc, _, err := scanEmailAccount(rows)
		if err != nil {
			return email.Page{}, err
		}
		accounts = append(accounts, acc)
	}
	if err := rows.Err(); err != nil {
		return email.Page{}, fmt.Errorf("iterate email accounts: %w", err)
	}

	page := email.Page{Accounts: accounts[:min(limit, len(accounts))]}
	if len(accounts) > limit {
		last := accounts[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "email_accounts", Time: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetEmailAccount(ctx context.Context, id string) (email.Account, error) {
	if !isUUID(id) {
		return email.Account{}, email.ErrInvalidArgument
	}
	row := s.pool.QueryRow(ctx, `
		SELECT `+emailAccountSelectCols+`
		FROM email_accounts WHERE id = $1`, id)
	acc, _, err := scanEmailAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return email.Account{}, email.ErrNotFound
	}
	if err != nil {
		return email.Account{}, fmt.Errorf("get email account: %w", err)
	}
	return acc, nil
}

func (s *Store) GetEmailAccountSecrets(ctx context.Context, id string) (email.AccountSecrets, []byte, error) {
	if !isUUID(id) {
		return email.AccountSecrets{}, nil, email.ErrInvalidArgument
	}
	row := s.pool.QueryRow(ctx, `
		SELECT `+emailAccountSelectCols+`
		FROM email_accounts WHERE id = $1`, id)
	acc, ciphertext, err := scanEmailAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return email.AccountSecrets{}, nil, email.ErrNotFound
	}
	if err != nil {
		return email.AccountSecrets{}, nil, fmt.Errorf("get email account secrets: %w", err)
	}
	return email.AccountSecrets{Account: acc}, ciphertext, nil
}

func (s *Store) CreateEmailAccount(ctx context.Context, params email.CreateParams, passwordCiphertext []byte) (email.Account, error) {
	if err := validateEmailAccountCreate(params, passwordCiphertext); err != nil {
		return email.Account{}, err
	}
	saveSent := email.DefaultSaveSentCopy(params.IMAPHost)
	if params.SaveSentCopy != nil {
		saveSent = *params.SaveSentCopy
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO email_accounts (
			name, address, username, password_ciphertext,
			imap_host, imap_port, imap_use_tls,
			smtp_host, smtp_port, smtp_use_tls, save_sent_copy, enabled
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING `+emailAccountSelectCols,
		params.Name, params.Address, params.Username, passwordCiphertext,
		params.IMAPHost, params.IMAPPort, params.IMAPTLS,
		params.SMTPHost, params.SMTPPort, params.SMTPTLS, saveSent, params.Enabled,
	)
	acc, _, err := scanEmailAccount(row)
	if err != nil {
		if isUniqueViolation(err) {
			return email.Account{}, email.ErrInvalidArgument
		}
		return email.Account{}, fmt.Errorf("create email account: %w", err)
	}
	return acc, nil
}

func (s *Store) UpdateEmailAccount(ctx context.Context, id string, params email.UpdateParams, passwordCiphertext []byte) (email.Account, error) {
	if !isUUID(id) {
		return email.Account{}, email.ErrInvalidArgument
	}
	current, err := s.GetEmailAccount(ctx, id)
	if err != nil {
		return email.Account{}, err
	}

	name := current.Name
	address := current.Address
	username := current.Username
	imapHost := current.IMAPHost
	imapPort := current.IMAPPort
	imapTLS := current.IMAPTLS
	smtpHost := current.SMTPHost
	smtpPort := current.SMTPPort
	smtpTLS := current.SMTPTLS
	saveSent := current.SaveSentCopy
	enabled := current.Enabled

	if params.Name != nil {
		name = strings.TrimSpace(*params.Name)
	}
	if params.Address != nil {
		address = strings.TrimSpace(*params.Address)
	}
	if params.Username != nil {
		username = strings.TrimSpace(*params.Username)
	}
	if params.IMAPHost != nil {
		imapHost = strings.TrimSpace(*params.IMAPHost)
	}
	if params.IMAPPort != nil {
		imapPort = *params.IMAPPort
	}
	if params.IMAPTLS != nil {
		imapTLS = *params.IMAPTLS
	}
	if params.SMTPHost != nil {
		smtpHost = strings.TrimSpace(*params.SMTPHost)
	}
	if params.SMTPPort != nil {
		smtpPort = *params.SMTPPort
	}
	if params.SMTPTLS != nil {
		smtpTLS = *params.SMTPTLS
	}
	if params.SaveSentCopy != nil {
		saveSent = *params.SaveSentCopy
	}
	if params.Enabled != nil {
		enabled = *params.Enabled
	}

	if name == "" || address == "" || username == "" || imapHost == "" || smtpHost == "" {
		return email.Account{}, email.ErrInvalidArgument
	}
	if _, err := mail.ParseAddress(address); err != nil {
		return email.Account{}, email.ErrInvalidArgument
	}
	if !validPort(imapPort) || !validPort(smtpPort) {
		return email.Account{}, email.ErrInvalidArgument
	}

	var row pgx.Row
	if passwordCiphertext != nil {
		row = s.pool.QueryRow(ctx, `
			UPDATE email_accounts SET
				name=$2, address=$3, username=$4, password_ciphertext=$5,
				imap_host=$6, imap_port=$7, imap_use_tls=$8,
				smtp_host=$9, smtp_port=$10, smtp_use_tls=$11, save_sent_copy=$12, enabled=$13,
				updated_at=now()
			WHERE id=$1
			RETURNING `+emailAccountSelectCols,
			id, name, address, username, passwordCiphertext,
			imapHost, imapPort, imapTLS, smtpHost, smtpPort, smtpTLS, saveSent, enabled,
		)
	} else {
		row = s.pool.QueryRow(ctx, `
			UPDATE email_accounts SET
				name=$2, address=$3, username=$4,
				imap_host=$5, imap_port=$6, imap_use_tls=$7,
				smtp_host=$8, smtp_port=$9, smtp_use_tls=$10, save_sent_copy=$11, enabled=$12,
				updated_at=now()
			WHERE id=$1
			RETURNING `+emailAccountSelectCols,
			id, name, address, username,
			imapHost, imapPort, imapTLS, smtpHost, smtpPort, smtpTLS, saveSent, enabled,
		)
	}
	acc, _, err := scanEmailAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return email.Account{}, email.ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return email.Account{}, email.ErrInvalidArgument
		}
		return email.Account{}, fmt.Errorf("update email account: %w", err)
	}
	return acc, nil
}

func (s *Store) DeleteEmailAccount(ctx context.Context, id string) error {
	if !isUUID(id) {
		return email.ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM email_accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete email account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return email.ErrNotFound
	}
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanEmailAccount(row scannable) (email.Account, []byte, error) {
	var acc email.Account
	var ciphertext []byte
	err := row.Scan(
		&acc.ID, &acc.Name, &acc.Address, &acc.Username, &ciphertext,
		&acc.IMAPHost, &acc.IMAPPort, &acc.IMAPTLS,
		&acc.SMTPHost, &acc.SMTPPort, &acc.SMTPTLS,
		&acc.SaveSentCopy, &acc.Enabled, &acc.CreatedAt, &acc.UpdatedAt,
	)
	return acc, ciphertext, err
}

func validateEmailAccountCreate(params email.CreateParams, passwordCiphertext []byte) error {
	if strings.TrimSpace(params.Name) == "" ||
		strings.TrimSpace(params.Address) == "" ||
		strings.TrimSpace(params.Username) == "" ||
		strings.TrimSpace(params.IMAPHost) == "" ||
		strings.TrimSpace(params.SMTPHost) == "" ||
		len(passwordCiphertext) == 0 {
		return email.ErrInvalidArgument
	}
	if _, err := mail.ParseAddress(params.Address); err != nil {
		return email.ErrInvalidArgument
	}
	if !validPort(params.IMAPPort) || !validPort(params.SMTPPort) {
		return email.ErrInvalidArgument
	}
	return nil
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "email_accounts_address_unique")
}
