-- +goose Up
-- Epic 53 story 53.16: each kept symbol table's kind and, for a native library, its whole GNU
-- build id, so the service admin's list tells an Apple dSYM's table ('dsym', keyed by the
-- binary's UUID) from an Android release's R8 mapping ('r8', keyed by its app and version)
-- and an Android native library's ('elf', keyed by the first 16 bytes of its build id).
-- NULL is a table kept before this column; the store fills it from the table itself the
-- first time the list is read (cloud/internal/symbols, Store.List).
ALTER TABLE symbol_tables ADD COLUMN IF NOT EXISTS kind TEXT;
ALTER TABLE symbol_tables ADD COLUMN IF NOT EXISTS code_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE symbol_tables DROP COLUMN IF EXISTS code_id;
ALTER TABLE symbol_tables DROP COLUMN IF EXISTS kind;
