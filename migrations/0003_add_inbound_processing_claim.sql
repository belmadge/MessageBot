-- +goose Up
ALTER TABLE messages
    ADD COLUMN processing_started_at TIMESTAMPTZ;

ALTER TABLE messages
    ADD CONSTRAINT messages_processing_started_inbound_only
        CHECK (processing_started_at IS NULL OR direction = 'inbound');

-- +goose Down
ALTER TABLE messages DROP CONSTRAINT messages_processing_started_inbound_only;
ALTER TABLE messages DROP COLUMN processing_started_at;
