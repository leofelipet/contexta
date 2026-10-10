package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/leofelipet/contexta/internal/passkeys"
)

var (
	passkeyIDPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	passkeyPublicKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	allowedPasskeyTransport = map[string]struct{}{
		"ble": {}, "cable": {}, "hybrid": {}, "internal": {}, "nfc": {}, "smart-card": {}, "usb": {},
	}
)

const adminPasskeySelect = `
	id, public_key, counter, transports, device_type, backed_up, name, created_at`

func (s *Store) ListAdminPasskeys(ctx context.Context) ([]passkeys.Passkey, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+adminPasskeySelect+` FROM admin_passkeys ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list admin passkeys: %w", err)
	}
	defer rows.Close()

	items := make([]passkeys.Passkey, 0)
	for rows.Next() {
		passkey, err := scanAdminPasskey(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, passkey)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin passkeys: %w", err)
	}
	return items, nil
}

func (s *Store) CreateAdminPasskey(ctx context.Context, params passkeys.CreateParams) (passkeys.Passkey, error) {
	validated, err := validateAdminPasskey(params)
	if err != nil {
		return passkeys.Passkey{}, err
	}

	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_passkeys`).Scan(&count); err != nil {
		return passkeys.Passkey{}, fmt.Errorf("count admin passkeys: %w", err)
	}
	if count >= passkeys.MaxPasskeys {
		return passkeys.Passkey{}, fmt.Errorf("%w: at most %d passkeys", ErrInvalidArgument, passkeys.MaxPasskeys)
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO admin_passkeys (id, public_key, counter, transports, device_type, backed_up, name)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+adminPasskeySelect,
		validated.ID, validated.PublicKey, validated.Counter, validated.Transports,
		validated.DeviceType, validated.BackedUp, validated.Name,
	)
	passkey, err := scanAdminPasskey(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return passkeys.Passkey{}, fmt.Errorf("%w: passkey already registered", ErrConflict)
		}
		return passkeys.Passkey{}, fmt.Errorf("create admin passkey: %w", err)
	}
	return passkey, nil
}

func (s *Store) UpdateAdminPasskeyCounter(ctx context.Context, id string, counter int64) (passkeys.Passkey, error) {
	if !passkeyIDPattern.MatchString(id) {
		return passkeys.Passkey{}, ErrInvalidArgument
	}
	if counter < 0 {
		return passkeys.Passkey{}, ErrInvalidArgument
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE admin_passkeys SET counter = $2 WHERE id = $1
		RETURNING `+adminPasskeySelect, id, counter)
	passkey, err := scanAdminPasskey(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return passkeys.Passkey{}, ErrNotFound
	}
	if err != nil {
		return passkeys.Passkey{}, fmt.Errorf("update admin passkey: %w", err)
	}
	return passkey, nil
}

func (s *Store) DeleteAdminPasskey(ctx context.Context, id string) error {
	if !passkeyIDPattern.MatchString(id) {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM admin_passkeys WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete admin passkey: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func validateAdminPasskey(params passkeys.CreateParams) (passkeys.CreateParams, error) {
	id := strings.TrimSpace(params.ID)
	publicKey := strings.TrimSpace(params.PublicKey)
	if !passkeyIDPattern.MatchString(id) || !passkeyPublicKeyPattern.MatchString(publicKey) || len(publicKey) > 4096 {
		return passkeys.CreateParams{}, ErrInvalidArgument
	}
	if params.Counter < 0 {
		return passkeys.CreateParams{}, ErrInvalidArgument
	}

	deviceType := strings.TrimSpace(params.DeviceType)
	if deviceType != "" && deviceType != "singleDevice" && deviceType != "multiDevice" {
		return passkeys.CreateParams{}, ErrInvalidArgument
	}

	name := strings.TrimSpace(params.Name)
	if name == "" {
		name = "Passkey"
	}
	if len(name) > 64 {
		return passkeys.CreateParams{}, ErrInvalidArgument
	}

	transports := make([]string, 0, len(params.Transports))
	seen := map[string]struct{}{}
	for _, transport := range params.Transports {
		transport = strings.TrimSpace(transport)
		if transport == "" {
			continue
		}
		if _, ok := allowedPasskeyTransport[transport]; !ok {
			return passkeys.CreateParams{}, ErrInvalidArgument
		}
		if _, ok := seen[transport]; ok {
			continue
		}
		seen[transport] = struct{}{}
		transports = append(transports, transport)
	}

	return passkeys.CreateParams{
		ID: id, PublicKey: publicKey, Counter: params.Counter,
		Transports: transports, DeviceType: deviceType, BackedUp: params.BackedUp, Name: name,
	}, nil
}

type adminPasskeyScanner interface {
	Scan(dest ...any) error
}

func scanAdminPasskey(row adminPasskeyScanner) (passkeys.Passkey, error) {
	var passkey passkeys.Passkey
	if err := row.Scan(
		&passkey.ID, &passkey.PublicKey, &passkey.Counter, &passkey.Transports,
		&passkey.DeviceType, &passkey.BackedUp, &passkey.Name, &passkey.CreatedAt,
	); err != nil {
		return passkeys.Passkey{}, err
	}
	if passkey.Transports == nil {
		passkey.Transports = []string{}
	}
	return passkey, nil
}
