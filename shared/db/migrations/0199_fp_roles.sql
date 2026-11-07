-- +goose Up
--
-- Slot 0199 - the Postgres roles of Maktaba's first-party plugins (Epic 54, story 54.24 §3),
-- written by tools/plugins/roles.py from the manifests (CI checks they agree: roles.py check).
--
-- Each maktaba_fp_<plugin> is NOLOGIN, with SELECT on its [trusted] db.read tables and
-- SELECT, INSERT, UPDATE, DELETE on its db.write tables and their sequences, nothing else
-- (every grant it had is revoked first). The plugin API runs the plugin's named statements
-- under it (SET LOCAL ROLE), so Postgres refuses any other table. The database owner and
-- maktaba_worker (every joined machine's login role) are members, so every machine's API may
-- SET ROLE; a member gains no right without it.
--
-- Wave 1 (story 54.28): tv.maktaba.classify.
--
-- Where the migration may not make the role (no right to create roles) or not grant it (a
-- role another database's owner made on a shared Postgres), it says so with a NOTICE and goes
-- on: the worker then runs those stages in-process (plugins/native.py, role_unusable).
--
-- Additive: roles and grants only; an older release never uses them.
--
-- tv.maktaba.classify: maktaba_fp_classify
-- +goose StatementBegin
DO $$
DECLARE
    t text;
    sq text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_fp_classify') THEN
        -- a home whose role may not create roles (Maktaba.app before its cluster
        -- bootstrap) has none: its stages run in-process (native.choose)
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user
                       AND (rolsuper OR rolcreaterole)) THEN
            RAISE NOTICE 'maktaba_fp_classify: not made (no right to create roles)';
            RETURN;
        END IF;
        CREATE ROLE maktaba_fp_classify NOLOGIN NOINHERIT;
    END IF;
    EXECUTE 'REVOKE ALL ON ALL TABLES IN SCHEMA public FROM maktaba_fp_classify';
    EXECUTE 'REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM maktaba_fp_classify';
    GRANT USAGE ON SCHEMA public TO maktaba_fp_classify;
    FOREACH t IN ARRAY ARRAY['app_settings', 'libraries', 'media_metadata_enrichment', 'processing_jobs', 'series', 'series_episodes', 'series_seasons', 'video_signals', 'video_topic_tags', 'videos', 'youtube_playlist_items', 'youtube_playlists', 'youtube_videos'] LOOP
        EXECUTE format('GRANT SELECT ON %I TO maktaba_fp_classify', t);
    END LOOP;
    FOREACH t IN ARRAY ARRAY['clip_group_items', 'clip_groups', 'promo_links', 'season_style', 'topic_collections', 'topic_items'] LOOP
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO maktaba_fp_classify', t);
        -- the table's own sequences (serial and identity columns)
        FOR sq IN SELECT s.relname FROM pg_class s JOIN pg_depend d ON d.objid = s.oid
                  JOIN pg_class c ON c.oid = d.refobjid
                 WHERE s.relkind = 'S' AND c.relname = t LOOP
            EXECUTE format('GRANT USAGE, SELECT, UPDATE ON SEQUENCE %I TO maktaba_fp_classify', sq);
        END LOOP;
    END LOOP;
    -- every machine's API may SET ROLE to it; membership alone gives no right
    -- (a role made by another database's owner on a shared Postgres may not be ours to
    -- grant: then this home's stages run in-process, native.choose)
    BEGIN
        EXECUTE format('GRANT maktaba_fp_classify TO %I', current_user);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
            BEGIN
                EXECUTE 'GRANT maktaba_fp_classify TO maktaba_worker WITH INHERIT FALSE';
            EXCEPTION WHEN syntax_error THEN
                EXECUTE 'GRANT maktaba_fp_classify TO maktaba_worker';
            END;
        END IF;
    EXCEPTION WHEN insufficient_privilege THEN
        RAISE NOTICE 'maktaba_fp_classify: not ours to grant';
    END;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
