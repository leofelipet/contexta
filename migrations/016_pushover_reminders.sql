-- +goose Up
ALTER TABLE task_schedules
    ADD COLUMN notify BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN notify_priority INTEGER NOT NULL DEFAULT 0 CHECK (notify_priority BETWEEN -2 AND 2),
    ADD COLUMN notify_sound TEXT NOT NULL DEFAULT '';

CREATE TABLE reminders (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0 CHECK (priority BETWEEN -2 AND 2),
    sound TEXT NOT NULL DEFAULT '',
    send_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX reminders_send_at_idx ON reminders (send_at DESC, id DESC);
CREATE INDEX reminders_due_idx ON reminders (send_at) WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS reminders;
ALTER TABLE task_schedules
    DROP COLUMN IF EXISTS notify_sound,
    DROP COLUMN IF EXISTS notify_priority,
    DROP COLUMN IF EXISTS notify;
