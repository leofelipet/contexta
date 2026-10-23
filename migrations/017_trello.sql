-- +goose Up
CREATE TABLE trello_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    api_key_ciphertext BYTEA NOT NULL,
    application_secret_ciphertext BYTEA NOT NULL,
    token_ciphertext BYTEA NOT NULL,
    board_id TEXT NOT NULL,
    list_statuses JSONB NOT NULL DEFAULT '{}'::jsonb,
    webhook_id TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE trello_card_links (
    card_id TEXT PRIMARY KEY,
    task_id BIGINT NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE RESTRICT,
    last_event_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE trello_card_links;
DROP TABLE trello_settings;
