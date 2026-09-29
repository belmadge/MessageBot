-- +goose Up
CREATE UNIQUE INDEX conversations_one_active_per_contact_uq
    ON conversations (contact_id)
    WHERE status <> 'resolved';

-- +goose Down
DROP INDEX conversations_one_active_per_contact_uq;
