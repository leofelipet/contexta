-- +goose Up
CREATE FUNCTION message_status_rank(value TEXT) RETURNS INTEGER
LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE lower(coalesce(value, ''))
        WHEN '' THEN 0
        WHEN 'queued' THEN 1
        WHEN 'pending' THEN 1
        WHEN 'sent' THEN 2
        WHEN 'delivered' THEN 3
        WHEN 'read' THEN 4
        WHEN 'failed' THEN 5
        WHEN 'canceled' THEN 5
        WHEN 'cancelled' THEN 5
        WHEN 'deleted' THEN 6
        ELSE 1
    END
$$;

CREATE TABLE pending_message_updates (
    provider_instance_id UUID NOT NULL REFERENCES provider_instances(id) ON DELETE CASCADE,
    provider_message_id TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider_instance_id, provider_message_id)
);

-- +goose Down
DROP TABLE pending_message_updates;
DROP FUNCTION message_status_rank(TEXT);
