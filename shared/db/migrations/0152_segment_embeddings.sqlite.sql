-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0152 (semantic search's vectors in Postgres, pgvector).
-- SQLite has no pgvector: there semantic search stays on Chroma until it is retired
-- (Epic 43), then off (full-text search). No segment_embeddings, no
-- semantic_store_state: the pipeline reads the store as chroma on SQLite.
-- This file keeps the slot occupied so the migration runner stays in lock-step on both
-- backends; the no-op SELECT keeps goose happy and is safe to re-apply.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
