-- +goose Up
ALTER TABLE messages
    ADD COLUMN external_id TEXT,
    ADD COLUMN reply_to_message_id BIGINT REFERENCES messages(id);

ALTER TABLE messages
    ADD CONSTRAINT messages_external_id_inbound_only
        CHECK (external_id IS NULL OR direction = 'inbound');

ALTER TABLE messages
    ADD CONSTRAINT messages_external_id_unique UNIQUE (external_id);

CREATE UNIQUE INDEX messages_reply_to_message_uq
    ON messages (reply_to_message_id)
    WHERE reply_to_message_id IS NOT NULL;

-- +goose Down
DROP INDEX messages_reply_to_message_uq;
ALTER TABLE messages DROP CONSTRAINT messages_external_id_unique;
ALTER TABLE messages DROP CONSTRAINT messages_external_id_inbound_only;
ALTER TABLE messages DROP COLUMN reply_to_message_id;
ALTER TABLE messages DROP COLUMN external_id;
