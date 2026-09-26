-- +goose Up
CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    company TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'in_progress', 'done', 'cancelled')),
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

-- +goose Down
DROP TABLE tasks;
