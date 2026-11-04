-- +goose Up
ALTER TABLE companies
    ADD COLUMN rules TEXT NOT NULL DEFAULT '',
    ADD COLUMN links JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE memories
    ADD COLUMN company_id BIGINT REFERENCES companies(id) ON DELETE SET NULL;
CREATE INDEX memories_company_id_idx ON memories (company_id);

ALTER TABLE conversations
    ADD COLUMN company_id BIGINT REFERENCES companies(id) ON DELETE SET NULL;
CREATE INDEX conversations_company_id_idx ON conversations (company_id);

-- +goose Down
DROP INDEX IF EXISTS conversations_company_id_idx;
ALTER TABLE conversations DROP COLUMN IF EXISTS company_id;

DROP INDEX IF EXISTS memories_company_id_idx;
ALTER TABLE memories DROP COLUMN IF EXISTS company_id;

ALTER TABLE companies DROP COLUMN IF EXISTS links;
ALTER TABLE companies DROP COLUMN IF EXISTS rules;
