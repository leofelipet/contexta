-- +goose Up
ALTER TABLE messages
    ADD COLUMN transcription_text TEXT NOT NULL DEFAULT '',
    ADD COLUMN transcription_status TEXT NOT NULL DEFAULT '',
    ADD COLUMN transcription_language TEXT NOT NULL DEFAULT '',
    ADD COLUMN transcription_model TEXT NOT NULL DEFAULT '',
    ADD COLUMN transcribed_at TIMESTAMPTZ;

DROP INDEX messages_search_idx;
ALTER TABLE messages DROP COLUMN search_vector;
ALTER TABLE messages ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (
    to_tsvector('simple'::regconfig, coalesce(text, '') || ' ' || coalesce(transcription_text, ''))
) STORED;
CREATE INDEX messages_search_idx ON messages USING GIN (search_vector);

CREATE TABLE transcription_jobs (
    message_id UUID PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'retry', 'completed', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX transcription_jobs_available_idx
    ON transcription_jobs (next_attempt_at, created_at)
    WHERE status IN ('pending', 'retry');

UPDATE messages SET type = 'audio'
WHERE lower(type) IN ('audiomessage', 'ptt', 'voice', 'voice_message');
UPDATE pending_message_updates SET type = 'audio'
WHERE lower(type) IN ('audiomessage', 'ptt', 'voice', 'voice_message');

UPDATE conversations SET title = ''
WHERE title LIKE '%@lid' OR title LIKE '%@s.whatsapp.net' OR title LIKE '%@g.us';
UPDATE contacts SET name = ''
WHERE name LIKE '%@lid' OR name LIKE '%@s.whatsapp.net' OR name LIKE '%@g.us';
UPDATE contacts SET push_name = ''
WHERE push_name LIKE '%@lid' OR push_name LIKE '%@s.whatsapp.net' OR push_name LIKE '%@g.us';

-- Historical provider metadata may contain signed media URLs and decryption keys.
UPDATE messages SET metadata = metadata - 'fileURL' - 'content'
WHERE metadata ? 'fileURL' OR metadata ? 'content';

-- +goose Down
DROP TABLE transcription_jobs;
DROP INDEX messages_search_idx;
ALTER TABLE messages DROP COLUMN search_vector;
ALTER TABLE messages
    DROP COLUMN transcription_text,
    DROP COLUMN transcription_status,
    DROP COLUMN transcription_language,
    DROP COLUMN transcription_model,
    DROP COLUMN transcribed_at;
ALTER TABLE messages ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (
    to_tsvector('simple'::regconfig, coalesce(text, ''))
) STORED;
CREATE INDEX messages_search_idx ON messages USING GIN (search_vector);
