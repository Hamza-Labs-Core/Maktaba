-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0062 (Epic 03 Story 3.6-4, HLB-324).
--
-- The `segments.committed` payload-key fix is a Postgres LISTEN/NOTIFY
-- concern. SQLite has no LISTEN/NOTIFY: the slot-0013 SQLite path never
-- created a DB trigger — `stt/segment_commit.py` publishes the
-- equivalent event on the in-process `get_bus()` after the SQLite
-- commit. The payload-key correction (`end_sec` ->
-- `last_segment_end_sec`) therefore lives entirely in that Python
-- publish call (changed in the same commit), not in SQL, so this
-- sibling is an intentional no-op kept only so the slot applies
-- cleanly on both backends (same shape as slot 0061's SQLite sibling).
--
-- This file uses goose's StatementBegin / StatementEnd markers.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
