-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'note'
        CHECK (source IN ('note', 'message')),
    message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    embedding vector(1536),
    embedding_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (embedding_status IN ('pending', 'ready', 'failed')),
    embedding_model TEXT NOT NULL DEFAULT '',
    embedding_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX memories_created_idx ON memories (created_at DESC, id DESC);
CREATE INDEX memories_conversation_id_idx ON memories (conversation_id);
CREATE INDEX memories_contact_id_idx ON memories (contact_id);
CREATE INDEX memories_message_id_idx ON memories (message_id);
CREATE INDEX memories_embedding_hnsw_idx ON memories USING hnsw (embedding vector_cosine_ops);

-- +goose Down
DROP TABLE IF EXISTS memories;
DROP EXTENSION IF EXISTS vector;
