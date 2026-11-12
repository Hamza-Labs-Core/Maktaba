-- +goose Up
-- +goose StatementBegin
--
-- Slot 0224 - tiers detected from transcripts (Epic 57, story 57.17).
--
--   video_content_signals  what the content_screen stage worked out for a video from its
--                          transcript, title, description and classification, on the
--                          home's own model: one row a video.
--     tier         0, 9, 13, 16, 18; NULL when it cannot tell or did not check.
--     confidence   0 to 1.
--     categories   {"violence": {"level": 0..3, "evidence": [seconds, up to three]}, ...}
--                  for violence, sexual_content, coarse_language, drugs_alcohol,
--                  frightening_scenes and mature_themes.
--     not_checked  why there is no answer: no_transcript or unsupported_language; NULL
--                  when the model was asked.
--     model, input_hash  what was asked and of what: the same pair is never asked twice.
--     reviewed_by, reviewed_at  an admin confirmed, changed or cleared the tier; a reviewed
--                  signal no longer rates the video (the admin's tier does), until the
--                  stage writes a new answer for a changed input.
--
--   videos.age_tier_source gains 'detected'.
--
-- The stage writes only this table, under its own role maktaba_fp_content_screen (written
-- by tools/plugins/roles.py, `stage-sql`; CI checks they agree): videos is read only for
-- it, so it cannot set videos.age_tier. The API's recompute (api/internal/parental) is
-- still the one writer of the tier. It counts a signal only at or above the line in
-- app_settings parental.detect_min_confidence (0.8 when the key is absent), and only to
-- tighten.
--
-- A signal written, changed or removed, and a change of the line, put a recompute job in
-- (the trigger functions are SECURITY DEFINER and not callable by hand, as in slot 0220).
--
-- The check on videos.age_tier_source is replaced NOT VALID and then validated, so the
-- table is not read under the exclusive lock. Every statement is harmless twice.
-- Additive: an older release never reads the table and never writes 'detected'.
--
CREATE TABLE IF NOT EXISTS video_content_signals (
    video_id    uuid        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    tier        smallint    CHECK (tier IS NULL OR tier IN (0, 9, 13, 16, 18)),
    confidence  real        NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
    categories  jsonb       NOT NULL DEFAULT '{}'::jsonb,
    not_checked text        CHECK (not_checked IS NULL
                                   OR not_checked IN ('no_transcript', 'unsupported_language')),
    model       text        NOT NULL DEFAULT '',
    input_hash  text        NOT NULL DEFAULT '',
    reviewed_by uuid        REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at timestamptz,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (not_checked IS NULL OR tier IS NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- the review list: what no admin has looked at yet, newest first
CREATE INDEX IF NOT EXISTS video_content_signals_review_idx
    ON video_content_signals (updated_at DESC, video_id)
    WHERE reviewed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conname = 'videos_age_tier_source_valid'
                      AND pg_get_constraintdef(oid) LIKE '%detected%') THEN
        ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_age_tier_source_valid;
        ALTER TABLE videos ADD CONSTRAINT videos_age_tier_source_valid
            CHECK (age_tier_source IS NULL OR age_tier_source IN ('admin_video', 'admin_container', 'plex',
                'guide', 'youtube', 'plugin', 'detected', 'library_default')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos VALIDATE CONSTRAINT videos_age_tier_source_valid;
-- +goose StatementEnd

-- +goose StatementBegin
-- A signal was written, changed or removed. Only what the tier is read from asks for a
-- recompute: the tier, the confidence and whether an admin reviewed it.
CREATE OR REPLACE FUNCTION content_signal_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.tier IS NOT NULL THEN
            PERFORM parental_recompute_enqueue('video', OLD.video_id::text);
        END IF;
        RETURN NULL;
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.tier IS NOT NULL THEN
            PERFORM parental_recompute_enqueue('video', NEW.video_id::text);
        END IF;
        RETURN NULL;
    END IF;
    IF NEW.tier IS DISTINCT FROM OLD.tier
       OR NEW.confidence IS DISTINCT FROM OLD.confidence
       OR NEW.reviewed_at IS DISTINCT FROM OLD.reviewed_at THEN
        PERFORM parental_recompute_enqueue('video', NEW.video_id::text);
    END IF;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER video_content_signals_recompute
    AFTER INSERT OR DELETE OR UPDATE OF tier, confidence, reviewed_at ON video_content_signals
    FOR EACH ROW EXECUTE FUNCTION content_signal_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- The line moved: every video with a signal may now be rated, or no longer.
CREATE OR REPLACE FUNCTION content_detect_line_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.value IS NOT DISTINCT FROM OLD.value THEN
        RETURN NULL;
    END IF;
    IF EXISTS (SELECT 1 FROM video_content_signals WHERE tier IS NOT NULL)
       AND NOT EXISTS (SELECT 1 FROM parental_recompute_jobs
                        WHERE kind = 'all' AND cursor_at IS NULL AND batches = 0) THEN
        PERFORM parental_recompute_enqueue('all', '');
    END IF;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER app_settings_detect_line_ins
    AFTER INSERT ON app_settings
    FOR EACH ROW WHEN (NEW.key = 'parental.detect_min_confidence')
    EXECUTE FUNCTION content_detect_line_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER app_settings_detect_line_upd
    AFTER UPDATE ON app_settings
    FOR EACH ROW WHEN (NEW.key = 'parental.detect_min_confidence')
    EXECUTE FUNCTION content_detect_line_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER app_settings_detect_line_del
    AFTER DELETE ON app_settings
    FOR EACH ROW WHEN (OLD.key = 'parental.detect_min_confidence')
    EXECUTE FUNCTION content_detect_line_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- trigger functions are never called by hand
REVOKE EXECUTE ON FUNCTION content_signal_changed(), content_detect_line_changed() FROM PUBLIC;
-- +goose StatementEnd

-- core stage content_screen: maktaba_fp_content_screen
-- +goose StatementBegin
DO $$
DECLARE
    t text;
    sq text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_fp_content_screen') THEN
        -- a home whose role may not create roles (Maktaba.app before its cluster
        -- bootstrap) has none: its stages run in-process (native.choose)
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user
                       AND (rolsuper OR rolcreaterole)) THEN
            RAISE NOTICE 'maktaba_fp_content_screen: not made (no right to create roles)';
            RETURN;
        END IF;
        CREATE ROLE maktaba_fp_content_screen NOLOGIN NOINHERIT;
    END IF;
    EXECUTE 'REVOKE ALL ON ALL TABLES IN SCHEMA public FROM maktaba_fp_content_screen';
    EXECUTE 'REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM maktaba_fp_content_screen';
    GRANT USAGE ON SCHEMA public TO maktaba_fp_content_screen;
    FOREACH t IN ARRAY ARRAY['app_settings', 'libraries', 'transcript_segments', 'transcripts', 'video_topic_tags', 'videos'] LOOP
        -- a table made later (segment_embeddings, with pgvector) is granted then
        CONTINUE WHEN to_regclass(format('public.%I', t)) IS NULL;
        EXECUTE format('GRANT SELECT ON %I TO maktaba_fp_content_screen', t);
    END LOOP;
    FOREACH t IN ARRAY ARRAY['video_content_signals'] LOOP
        CONTINUE WHEN to_regclass(format('public.%I', t)) IS NULL;
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO maktaba_fp_content_screen', t);
        -- the table's own sequences (serial and identity columns)
        FOR sq IN SELECT s.relname FROM pg_class s JOIN pg_depend d ON d.objid = s.oid
                  JOIN pg_class c ON c.oid = d.refobjid
                 WHERE s.relkind = 'S' AND c.relname = t LOOP
            EXECUTE format('GRANT USAGE, SELECT, UPDATE ON SEQUENCE %I TO maktaba_fp_content_screen', sq);
        END LOOP;
    END LOOP;
    -- every machine's API may SET ROLE to it; membership alone gives no right
    -- (a role made by another database's owner on a shared Postgres may not be ours to
    -- grant: then this home's stages run in-process, native.choose)
    BEGIN
        EXECUTE format('GRANT maktaba_fp_content_screen TO %I', current_user);
        IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
            BEGIN
                EXECUTE 'GRANT maktaba_fp_content_screen TO maktaba_worker WITH INHERIT FALSE';
            EXCEPTION WHEN syntax_error THEN
                EXECUTE 'GRANT maktaba_fp_content_screen TO maktaba_worker';
            END;
        END IF;
    EXCEPTION WHEN insufficient_privilege THEN
        RAISE NOTICE 'maktaba_fp_content_screen: not ours to grant';
    END;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- the content_screen stage (Epic 57, story 57.17): video and library subjects, the cpu
-- pool, speech lane 10, on the home only; its edges are the pipeline's to write (its graph
-- sync). The Down leaves the row, as every stage seed does: the queue's rows name it.
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('content_screen', 'Content screen', ARRAY['video', 'library']::text[], 'cpu', 'speech', 10, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home', true, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_settings_detect_line_del ON app_settings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_settings_detect_line_upd ON app_settings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_settings_detect_line_ins ON app_settings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS content_detect_line_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- the table goes with its trigger, its index and the role's grants on it (the role
-- itself stays, with nothing granted: roles belong to the whole Postgres, not this database)
DROP TABLE IF EXISTS video_content_signals;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS content_signal_changed();
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM app_settings WHERE key = 'parental.detect_min_confidence';
-- +goose StatementEnd

-- +goose StatementBegin
-- No tier may stay that nothing gives any more: a detected tier is cleared, and the one
-- recompute of every video rates those videos again from what is left (a library default).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM videos WHERE age_tier_source = 'detected') THEN
        UPDATE videos SET age_tier = NULL, age_tier_source = NULL WHERE age_tier_source = 'detected';
        INSERT INTO parental_recompute_jobs (kind) VALUES ('all');
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_age_tier_source_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD CONSTRAINT videos_age_tier_source_valid
    CHECK (age_tier_source IS NULL OR age_tier_source IN ('admin_video', 'admin_container', 'plex',
        'guide', 'youtube', 'plugin', 'library_default'));
-- +goose StatementEnd
