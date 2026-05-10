-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0055 (plan-15-05 / plan-15-06) — pairing_tickets table.
--
-- Short-lived (≤5 min) QR-pairing codes. The TV requests one, displays
-- it as a QR; the phone scans → POST /api/pairing/exchange consumes
-- the ticket and returns the linked user id.
--
CREATE TABLE IF NOT EXISTS pairing_tickets (
    code         TEXT         PRIMARY KEY,
    user_id      UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ  NOT NULL,
    consumed_at  TIMESTAMPTZ,
    consumed_by  UUID         REFERENCES devices(id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS pairing_tickets_user
    ON pairing_tickets (user_id, issued_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS pairing_tickets_reaper
    ON pairing_tickets (expires_at) WHERE consumed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS pairing_tickets_reaper;
DROP INDEX IF EXISTS pairing_tickets_user;
DROP TABLE IF EXISTS pairing_tickets;
-- +goose StatementEnd
