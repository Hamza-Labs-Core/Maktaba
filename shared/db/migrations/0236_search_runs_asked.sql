-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0236 (Epic 49, story 49.10 follow-up): a search keeps how it was asked, so a
-- Recent search opens with its order and filters on any device.
--
--   sort_order     the order asked for: relevance, newest or matches
--   asked_filters  the filters as asked (JSON text): `filters` keeps them after a
--                  restricted account's libraries were put in, which are not a choice
--
-- Both are NULL on a row written before this slot (and by a release before it): the
-- API then leaves them out of its answer, and the client opens the search as before.
-- Nullable columns with no default: no rewrite, no row is changed, and a rollback keeps
-- them (the previous release does not name them). Additive.
--
ALTER TABLE search_runs ADD COLUMN IF NOT EXISTS sort_order TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE search_runs ADD COLUMN IF NOT EXISTS asked_filters TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE search_runs DROP COLUMN IF EXISTS asked_filters;
ALTER TABLE search_runs DROP COLUMN IF EXISTS sort_order;
-- +goose StatementEnd
