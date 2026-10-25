-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0151: the machine's report (full_capable) and the
-- admin's decision (full_allowed) beside full_node (a home on SQLite takes no machines).
--
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS full_capable INTEGER NOT NULL DEFAULT 0;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS full_allowed INTEGER NOT NULL DEFAULT 0;
UPDATE cluster_nodes SET full_capable = full_node, full_allowed = full_node;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE cluster_nodes DROP COLUMN full_allowed;
ALTER TABLE cluster_nodes DROP COLUMN full_capable;
-- +goose StatementEnd
