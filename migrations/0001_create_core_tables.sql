-- +goose Up
CREATE TABLE contacts (
    id BIGSERIAL PRIMARY KEY,
    phone TEXT NOT NULL UNIQUE,
    name TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE conversations (
    id BIGSERIAL PRIMARY KEY,
    contact_id BIGINT NOT NULL REFERENCES contacts(id),
    status TEXT NOT NULL DEFAULT 'bot' CHECK (status IN ('bot', 'human', 'resolved')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX conversations_status_updated_idx ON conversations(status, updated_at DESC);
CREATE INDEX conversations_contact_status_idx ON conversations(contact_id, status);

CREATE TABLE messages (
    id BIGSERIAL PRIMARY KEY,
    conversation_id BIGINT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    direction TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    content TEXT NOT NULL CHECK (length(trim(content)) > 0),
    sender TEXT NOT NULL CHECK (sender IN ('contact', 'ai', 'human')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX messages_conversation_created_idx ON messages(conversation_id, created_at, id);

-- +goose Down
DROP TABLE messages;
DROP TABLE conversations;
DROP TABLE contacts;
