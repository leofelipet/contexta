-- +goose Up
CREATE TABLE admin_passkeys (
    id TEXT PRIMARY KEY,
    public_key TEXT NOT NULL,
    counter BIGINT NOT NULL DEFAULT 0 CHECK (counter >= 0),
    transports TEXT[] NOT NULL DEFAULT '{}',
    device_type TEXT NOT NULL DEFAULT '',
    backed_up BOOLEAN NOT NULL DEFAULT FALSE,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX admin_passkeys_created_idx ON admin_passkeys (created_at DESC, id);

-- +goose Down
DROP TABLE admin_passkeys;
