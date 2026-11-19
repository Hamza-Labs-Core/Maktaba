-- +goose Up
-- Slot 0231 (Epic 49 story 49.4): SQLite has no SQL functions or text search configurations
-- of its own. Its keyword search normalises in the application (api/internal/textnorm,
-- the same folding as Postgres's maktaba_normalize, a test keeps the two equal) before FTS5
-- indexes or matches a text, and ranks with FTS5's bm25(); word forms (maktaba_stem) are
-- Postgres only (specs/epics/49-search/README.md §4).
SELECT 1;

-- +goose Down
SELECT 1;
