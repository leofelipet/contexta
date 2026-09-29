-- +goose Up
CREATE TABLE task_schedules (
    id BIGSERIAL PRIMARY KEY,
    cron TEXT NOT NULL,
    timezone TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    skip_if_open BOOLEAN NOT NULL DEFAULT false,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    company TEXT NOT NULL DEFAULT '',
    due_in_minutes INTEGER CHECK (due_in_minutes IS NULL OR due_in_minutes > 0),
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    next_run_at TIMESTAMPTZ,
    last_run_at TIMESTAMPTZ,
    last_task_id BIGINT REFERENCES tasks(id) ON DELETE SET NULL,
    run_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX task_schedules_created_idx ON task_schedules (created_at DESC, id DESC);
CREATE INDEX task_schedules_due_idx ON task_schedules (next_run_at) WHERE enabled;

ALTER TABLE tasks ADD COLUMN schedule_id BIGINT REFERENCES task_schedules(id) ON DELETE SET NULL;
CREATE INDEX tasks_schedule_id_idx ON tasks (schedule_id);

-- +goose Down
ALTER TABLE tasks DROP COLUMN IF EXISTS schedule_id;
DROP TABLE IF EXISTS task_schedules;
