-- +goose Up
CREATE TABLE activity_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category TEXT NOT NULL,
    level TEXT NOT NULL CHECK (level IN ('info', 'warning', 'error')),
    operation TEXT NOT NULL,
    outcome TEXT NOT NULL,
    entity_type TEXT NOT NULL DEFAULT '',
    entity_id TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX activity_events_time_idx ON activity_events (occurred_at DESC, id DESC);
CREATE INDEX activity_events_category_time_idx ON activity_events (category, occurred_at DESC);
CREATE INDEX activity_events_level_time_idx ON activity_events (level, occurred_at DESC);

-- +goose Down
DROP TABLE activity_events;
