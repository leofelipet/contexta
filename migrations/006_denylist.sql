-- +goose Up
CREATE TABLE denylist_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type TEXT NOT NULL CHECK (target_type IN ('conversation', 'contact')),
    target_id UUID NOT NULL,
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (target_type, target_id)
);

CREATE INDEX denylist_entries_created_idx ON denylist_entries (created_at DESC, id DESC);

-- +goose Down
DROP TABLE denylist_entries;
