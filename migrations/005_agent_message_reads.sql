-- +goose Up
CREATE TABLE message_read_receipts (
    consumer_id TEXT NOT NULL,
    message_id UUID NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_id, message_id),
    CHECK (consumer_id ~ '^[A-Za-z0-9._:-]{1,64}$')
);

CREATE INDEX message_read_receipts_message_idx
    ON message_read_receipts (message_id);

-- +goose Down
DROP TABLE message_read_receipts;
