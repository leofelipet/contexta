package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/mcpkeys"
)

const (
	mcpKeySelect = `id::text, name, prefix, access, tools, created_at, expires_at, last_used_at`
	maxMCPKeys   = 100
)

var mcpToolNamePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

func (s *Store) ListMCPKeys(ctx context.Context) ([]mcpkeys.Key, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mcpKeySelect+` FROM mcp_keys ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list mcp keys: %w", err)
	}
	defer rows.Close()
	items := make([]mcpkeys.Key, 0)
	for rows.Next() {
		key, err := scanMCPKey(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp keys: %w", err)
	}
	return items, nil
}

func (s *Store) CreateMCPKey(ctx context.Context, params mcpkeys.Params, prefix, hash string) (mcpkeys.Key, error) {
	params, err := validateMCPKey(params)
	if err != nil {
		return mcpkeys.Key{}, err
	}
	if params.ExpiresAt != nil && !params.ExpiresAt.After(time.Now()) {
		return mcpkeys.Key{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidArgument)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM mcp_keys`).Scan(&count); err != nil {
		return mcpkeys.Key{}, fmt.Errorf("count mcp keys: %w", err)
	}
	if count >= maxMCPKeys {
		return mcpkeys.Key{}, fmt.Errorf("%w: at most %d keys", ErrInvalidArgument, maxMCPKeys)
	}
	key, err := scanMCPKey(s.pool.QueryRow(ctx, `
		INSERT INTO mcp_keys (name, prefix, key_hash, access, tools, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+mcpKeySelect, params.Name, prefix, hash, params.Access, params.Tools, params.ExpiresAt))
	if err != nil {
		return mcpkeys.Key{}, fmt.Errorf("create mcp key: %w", err)
	}
	return key, nil
}

// UpdateMCPKey changes name and scope; the secret and expiry stay as created.
func (s *Store) UpdateMCPKey(ctx context.Context, id string, params mcpkeys.Params) (mcpkeys.Key, error) {
	if !isUUID(id) {
		return mcpkeys.Key{}, ErrNotFound
	}
	params, err := validateMCPKey(params)
	if err != nil {
		return mcpkeys.Key{}, err
	}
	key, err := scanMCPKey(s.pool.QueryRow(ctx, `
		UPDATE mcp_keys SET name = $2, access = $3, tools = $4 WHERE id = $1
		RETURNING `+mcpKeySelect, id, params.Name, params.Access, params.Tools))
	if errors.Is(err, pgx.ErrNoRows) {
		return mcpkeys.Key{}, ErrNotFound
	}
	if err != nil {
		return mcpkeys.Key{}, fmt.Errorf("update mcp key: %w", err)
	}
	return key, nil
}

func (s *Store) DeleteMCPKey(ctx context.Context, id string) error {
	if !isUUID(id) {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM mcp_keys WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete mcp key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthenticateMCPKey returns the unexpired key with this hash and stamps its last use.
func (s *Store) AuthenticateMCPKey(ctx context.Context, hash string) (mcpkeys.Key, error) {
	key, err := scanMCPKey(s.pool.QueryRow(ctx, `
		UPDATE mcp_keys SET last_used_at = now()
		WHERE key_hash = $1 AND (expires_at IS NULL OR expires_at > now())
		RETURNING `+mcpKeySelect, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return mcpkeys.Key{}, ErrNotFound
	}
	if err != nil {
		return mcpkeys.Key{}, fmt.Errorf("authenticate mcp key: %w", err)
	}
	return key, nil
}

func validateMCPKey(params mcpkeys.Params) (mcpkeys.Params, error) {
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" || len(params.Name) > 64 {
		return params, fmt.Errorf("%w: name must have 1 to 64 characters", ErrInvalidArgument)
	}
	switch params.Access {
	case mcpkeys.AccessRead, mcpkeys.AccessWrite:
		params.Tools = []string{}
		return params, nil
	case mcpkeys.AccessCustom:
	default:
		return params, fmt.Errorf("%w: access must be read, write, or custom", ErrInvalidArgument)
	}
	tools := make([]string, 0, len(params.Tools))
	seen := map[string]bool{}
	for _, tool := range params.Tools {
		if !mcpToolNamePattern.MatchString(tool) {
			return params, fmt.Errorf("%w: invalid tool name", ErrInvalidArgument)
		}
		if !seen[tool] {
			seen[tool] = true
			tools = append(tools, tool)
		}
	}
	if len(tools) == 0 || len(tools) > 200 {
		return params, fmt.Errorf("%w: custom access needs at least one tool", ErrInvalidArgument)
	}
	params.Tools = tools
	return params, nil
}

func scanMCPKey(row pgx.Row) (mcpkeys.Key, error) {
	var key mcpkeys.Key
	if err := row.Scan(&key.ID, &key.Name, &key.Prefix, &key.Access, &key.Tools,
		&key.CreatedAt, &key.ExpiresAt, &key.LastUsedAt); err != nil {
		return mcpkeys.Key{}, err
	}
	if key.Tools == nil {
		key.Tools = []string{}
	}
	return key, nil
}
