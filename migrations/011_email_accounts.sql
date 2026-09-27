-- +goose Up
CREATE TABLE email_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    address TEXT NOT NULL,
    username TEXT NOT NULL,
    password_ciphertext BYTEA NOT NULL,
    imap_host TEXT NOT NULL,
    imap_port INT NOT NULL CHECK (imap_port BETWEEN 1 AND 65535),
    imap_use_tls BOOLEAN NOT NULL DEFAULT TRUE,
    smtp_host TEXT NOT NULL,
    smtp_port INT NOT NULL CHECK (smtp_port BETWEEN 1 AND 65535),
    smtp_use_tls BOOLEAN NOT NULL DEFAULT TRUE,
    save_sent_copy BOOLEAN NOT NULL DEFAULT TRUE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT email_accounts_address_unique UNIQUE (address)
);

CREATE INDEX email_accounts_enabled_idx ON email_accounts (enabled);
CREATE INDEX email_accounts_created_idx ON email_accounts (created_at DESC, id DESC);

-- +goose Down
DROP TABLE email_accounts;
