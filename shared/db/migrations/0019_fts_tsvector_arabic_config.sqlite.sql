-- +goose Up
-- +goose StatementBegin
-- SQLite has no text search configurations. The application-level
-- `arabic_normalize()` Python function in
-- `pipeline/src/maktaba_pipeline/search/fts/normalize.py` does the
-- equivalent normalization. The actual FTS5 virtual table is defined in
-- slot 0020 with `tokenize = 'unicode61 remove_diacritics 2'`.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
