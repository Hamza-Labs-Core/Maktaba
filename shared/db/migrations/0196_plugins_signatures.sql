-- +goose Up
-- +goose StatementBegin
--
-- Slot 0196 - plugins: what a package's signatures prove (Epic 54, story 54.14).
--
--   plugin_installs.review_level   also 'automated' and 'reviewed', the levels a store
--                                  countersignature (store.sig) records (54.14 §2);
--                                  'store' (slot 0194's name for the same) stays allowed
--                                  and is never written again
--   plugin_installs.developer_key_id  the developer key that signed the installed version
--                                  ('' when unsigned), matched by a revocation entry that
--                                  names a key (54.17 §3)
--   plugin_installs.reviewed_at    the store's review time from store.sig
--   plugin_installs.previous_*     the same three of the previous version, so a rollback
--                                  takes the version's own level back with it
--
-- The check is widened, never narrowed: every value the previous release writes is still
-- allowed, so rolling back is safe (a previous release reads 'reviewed' as unreviewed and
-- holds the plugin under its policy, which an admin can allow). Additive.
--
ALTER TABLE plugin_installs DROP CONSTRAINT IF EXISTS plugin_installs_review_level_check;
ALTER TABLE plugin_installs ADD CONSTRAINT plugin_installs_review_level_check
    CHECK (review_level IN ('none', 'developer', 'store', 'automated', 'reviewed', 'development'));
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS developer_key_id text NOT NULL DEFAULT '';
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS reviewed_at timestamptz;
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS previous_review_level text;
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS previous_developer_key_id text NOT NULL DEFAULT '';
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS previous_reviewed_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE plugin_installs DROP COLUMN IF EXISTS previous_reviewed_at;
ALTER TABLE plugin_installs DROP COLUMN IF EXISTS previous_developer_key_id;
ALTER TABLE plugin_installs DROP COLUMN IF EXISTS previous_review_level;
ALTER TABLE plugin_installs DROP COLUMN IF EXISTS reviewed_at;
ALTER TABLE plugin_installs DROP COLUMN IF EXISTS developer_key_id;
-- +goose StatementEnd
