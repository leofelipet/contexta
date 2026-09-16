-- +goose Up
CREATE TABLE provider_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    provider_instance_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_instance_id)
);

CREATE TABLE contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_instance_id UUID NOT NULL REFERENCES provider_instances(id) ON DELETE CASCADE,
    provider_contact_id TEXT NOT NULL,
    phone TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    push_name TEXT NOT NULL DEFAULT '',
    profile_picture_url TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider_instance_id, provider_contact_id)
);

CREATE TABLE contact_identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_instance_id UUID NOT NULL REFERENCES provider_instances(id) ON DELETE CASCADE,
    contact_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    identity TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider_instance_id, identity)
);

CREATE TABLE conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_instance_id UUID NOT NULL REFERENCES provider_instances(id) ON DELETE CASCADE,
    provider_conversation_id TEXT NOT NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    type TEXT NOT NULL CHECK (type IN ('direct', 'group', 'newsletter', 'unknown')),
    title TEXT NOT NULL DEFAULT '',
    last_message_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider_instance_id, provider_conversation_id)
);

CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_instance_id UUID NOT NULL REFERENCES provider_instances(id) ON DELETE CASCADE,
    provider_message_id TEXT NOT NULL,
    provider_record_id TEXT NOT NULL DEFAULT '',
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    sender_contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL,
    direction TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    type TEXT NOT NULL DEFAULT 'unknown',
    text TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    reply_to_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
    reply_to_provider_message_id TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    search_vector TSVECTOR GENERATED ALWAYS AS (
        to_tsvector('simple'::regconfig, coalesce(text, ''))
    ) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider_instance_id, provider_message_id)
);

CREATE INDEX contacts_name_idx ON contacts (lower(name), id);
CREATE INDEX contact_identities_contact_idx ON contact_identities (contact_id);
CREATE INDEX conversations_last_message_idx ON conversations (last_message_at DESC NULLS LAST, id DESC);
CREATE INDEX conversations_contact_idx ON conversations (contact_id);
CREATE INDEX messages_conversation_time_idx ON messages (conversation_id, occurred_at DESC, id DESC);
CREATE INDEX messages_sender_time_idx ON messages (sender_contact_id, occurred_at DESC);
CREATE INDEX messages_occurred_at_idx ON messages (occurred_at DESC, id DESC);
CREATE INDEX messages_search_idx ON messages USING GIN (search_vector);

-- +goose Down
DROP TABLE messages;
DROP TABLE conversations;
DROP TABLE contact_identities;
DROP TABLE contacts;
DROP TABLE provider_instances;
