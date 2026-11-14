-- +goose Up
-- +goose StatementBegin
--
-- Slot 0226 - the cloud account that approved a TV's code (Epic 58, story 58.03).
--
-- A TV's cloud code approved on maktaba.tv signs it in as the home user that cloud
-- account is linked to. The approval is a pairing ticket the TV redeems a moment later;
-- the home records the TV's sign-in as one of the link's (unlinking ends it) only while
-- the link still names the account that approved it. pairing_tickets.cloud_user_id is
-- that account: NULL for every other approval, and for a cloud code approved by a
-- release before this one (its TV is refused and asks for a new code).
--
-- A nullable column with no default: no rewrite, no lock held beyond the catalog
-- change, and a release before this one reads and writes the table as before.
--
ALTER TABLE pairing_tickets ADD COLUMN IF NOT EXISTS cloud_user_id TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- the column holds only live approvals (ten minutes): a release before this one has no use for it
ALTER TABLE pairing_tickets DROP COLUMN IF EXISTS cloud_user_id;
-- +goose StatementEnd
