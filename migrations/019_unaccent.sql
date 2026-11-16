-- +goose Up
CREATE EXTENSION IF NOT EXISTS unaccent;

-- Same as 'simple' but strips accents, so message search ignores them.
CREATE TEXT SEARCH CONFIGURATION simple_unaccent (COPY = simple);
ALTER TEXT SEARCH CONFIGURATION simple_unaccent
    ALTER MAPPING FOR hword, hword_part, word WITH unaccent, simple;

DROP INDEX messages_search_idx;
ALTER TABLE messages DROP COLUMN search_vector;
ALTER TABLE messages ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (
    to_tsvector('simple_unaccent'::regconfig, coalesce(text, '') || ' ' || coalesce(transcription_text, ''))
) STORED;
CREATE INDEX messages_search_idx ON messages USING GIN (search_vector);

-- +goose Down
DROP INDEX messages_search_idx;
ALTER TABLE messages DROP COLUMN search_vector;
ALTER TABLE messages ADD COLUMN search_vector TSVECTOR GENERATED ALWAYS AS (
    to_tsvector('simple'::regconfig, coalesce(text, '') || ' ' || coalesce(transcription_text, ''))
) STORED;
CREATE INDEX messages_search_idx ON messages USING GIN (search_vector);
DROP TEXT SEARCH CONFIGURATION simple_unaccent;
DROP EXTENSION IF EXISTS unaccent;
