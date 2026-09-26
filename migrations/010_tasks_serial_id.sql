-- +goose Up
-- Recreate tasks with sequential IDs (safe while the table is empty / no production task data).
DROP TABLE IF EXISTS task_memories;
DROP TABLE IF EXISTS tasks;

CREATE TABLE tasks (
    id BIGSERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    company TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'in_progress', 'blocked', 'done', 'cancelled')),
    due_at TIMESTAMPTZ,
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tasks_created_idx ON tasks (created_at DESC, id DESC);
CREATE INDEX tasks_status_idx ON tasks (status);
CREATE INDEX tasks_company_idx ON tasks (company);
CREATE INDEX tasks_due_at_idx ON tasks (due_at);
CREATE INDEX tasks_conversation_id_idx ON tasks (conversation_id);
CREATE INDEX tasks_contact_id_idx ON tasks (contact_id);

CREATE TABLE task_memories (
    task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    memory_id UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, memory_id)
);

CREATE INDEX task_memories_memory_id_idx ON task_memories (memory_id);

-- +goose Down
DROP TABLE IF EXISTS task_memories;
DROP TABLE IF EXISTS tasks;

CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    company TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'in_progress', 'blocked', 'done', 'cancelled')),
    due_at TIMESTAMPTZ,
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX tasks_created_idx ON tasks (created_at DESC, id DESC);
CREATE INDEX tasks_status_idx ON tasks (status);
CREATE INDEX tasks_company_idx ON tasks (company);
CREATE INDEX tasks_due_at_idx ON tasks (due_at);
CREATE INDEX tasks_conversation_id_idx ON tasks (conversation_id);
CREATE INDEX tasks_contact_id_idx ON tasks (contact_id);

CREATE TABLE task_memories (
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    memory_id UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, memory_id)
);

CREATE INDEX task_memories_memory_id_idx ON task_memories (memory_id);
