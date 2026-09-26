-- +goose Up
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('pending', 'in_progress', 'blocked', 'done', 'cancelled'));

CREATE TABLE task_memories (
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    memory_id UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, memory_id)
);

CREATE INDEX task_memories_memory_id_idx ON task_memories (memory_id);

-- +goose Down
DROP TABLE IF EXISTS task_memories;

ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
    CHECK (status IN ('pending', 'in_progress', 'done', 'cancelled'));
