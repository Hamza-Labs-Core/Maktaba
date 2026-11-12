-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0220 - age tiers on the home's content (Epic 57, story 57.01).
--
--   videos.age_tier         the video's tier: 0 (all ages), 9, 13, 16, 18; NULL is unrated.
--   videos.age_tier_source  where it came from: admin_video, admin_container, plex, guide,
--                           youtube, plugin, library_default; NULL with a NULL tier.
--   videos.age_tier_admin   the tier an admin set on the video itself (NULL: none).
--   libraries.age_tier      the library's default tier for a video nothing else rates.
--   series.age_tier, collections.age_tier, youtube_sources.age_tier
--                           an admin's tier on a container: its videos take the strictest
--                           of their containers' tiers.
--   channels.age_tier       an admin's tier on a live channel, read where channels are
--                           listed and played (story 57.10); a channel's programmes are a
--                           rolling schedule, so it does not flow into videos.age_tier.
--   channel_programs.age_tier  a programme's tier (story 57.10 writes it).
--   youtube_videos.age_limit   yt-dlp's age_limit (0 or 18), kept by the YouTube stages.
--
-- videos.age_tier and age_tier_source have one writer: the API's recompute
-- (api/internal/parental). Everything else says what changed and the recompute reads it:
--
--   parental_recompute_jobs  the recompute's queue. kind 'video' names one video; 'library',
--                            'series', 'collection', 'youtube_source' and 'youtube_video'
--                            name what contains or matches the videos; 'all' is every
--                            library. A job is worked in batches of 1,000 videos, newest
--                            first, its place kept in cursor_at and cursor_id, and deleted
--                            when done. Working a job twice gives the same tiers. attempts
--                            counts the batches of the job that failed: the queue is worked
--                            by (attempts, batches, id), so a job that keeps failing goes
--                            behind the others instead of holding them up, and is never
--                            dropped.
--
-- The stages run under their own roles and write no tier, so triggers put the jobs in
-- (the trigger functions are SECURITY DEFINER: a stage's role needs no grant on the
-- queue, and nobody but the owner may call parental_recompute_enqueue): a new video, a video moved to another library, a changed
-- metadata.content_rating (a plugin's set-field), a video joining or leaving a
-- collection, a series or a YouTube source, a YouTube match made or dropped, a
-- learned age_limit, and a kept Plex item's or a guide programme's rating or video
-- changing or going. A trigger asks for a recompute only where the tier can change.
--
-- One 'all' job is put in here, so the ratings a home already has (kept Plex ratings,
-- plugins' ratings) fill in after the update, in the background.
--
-- NO TRANSACTION for the two indexes (CONCURRENTLY: videos is large). The three checks on
-- videos are added NOT VALID and validated in a statement of their own, so the table is
-- read under a lock that blocks no reader or writer (as slot 0224 does); the other
-- tables are small. Every statement is harmless twice. Additive: an older release never
-- reads the columns or the table.
--
ALTER TABLE videos ADD COLUMN IF NOT EXISTS age_tier smallint;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'videos_age_tier_valid') THEN
        ALTER TABLE videos ADD CONSTRAINT videos_age_tier_valid
            CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18)) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos VALIDATE CONSTRAINT videos_age_tier_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS age_tier_source text;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'videos_age_tier_source_valid') THEN
        ALTER TABLE videos ADD CONSTRAINT videos_age_tier_source_valid
            CHECK (age_tier_source IS NULL OR age_tier_source IN ('admin_video', 'admin_container', 'plex',
                'guide', 'youtube', 'plugin', 'library_default')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos VALIDATE CONSTRAINT videos_age_tier_source_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS age_tier_admin smallint;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'videos_age_tier_admin_valid') THEN
        ALTER TABLE videos ADD CONSTRAINT videos_age_tier_admin_valid
            CHECK (age_tier_admin IS NULL OR age_tier_admin IN (0, 9, 13, 16, 18)) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos VALIDATE CONSTRAINT videos_age_tier_admin_valid;
-- +goose StatementEnd

-- +goose StatementBegin
-- An index build that was cut short (the app quit, the machine slept) leaves the index
-- there but INVALID: never used, kept up on every write, and skipped by IF NOT EXISTS
-- below. It is dropped so that the build runs again.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
                WHERE c.relname = 'videos_library_age_tier_idx' AND c.relnamespace = 'public'::regnamespace
                  AND NOT i.indisvalid) THEN
        DROP INDEX IF EXISTS videos_library_age_tier_idx;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- a library's videos at or under a tier (the filter of story 57.03)
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_age_tier_idx
    ON videos (library_id, age_tier);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS age_tier smallint;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'libraries_age_tier_valid') THEN
        ALTER TABLE libraries ADD CONSTRAINT libraries_age_tier_valid
            CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE series ADD COLUMN IF NOT EXISTS age_tier smallint;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'series_age_tier_valid') THEN
        ALTER TABLE series ADD CONSTRAINT series_age_tier_valid
            CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS age_tier smallint;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'collections_age_tier_valid') THEN
        ALTER TABLE collections ADD CONSTRAINT collections_age_tier_valid
            CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels ADD COLUMN IF NOT EXISTS age_tier smallint;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'channels_age_tier_valid') THEN
        ALTER TABLE channels ADD CONSTRAINT channels_age_tier_valid
            CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_sources ADD COLUMN IF NOT EXISTS age_tier smallint;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'youtube_sources_age_tier_valid') THEN
        ALTER TABLE youtube_sources ADD CONSTRAINT youtube_sources_age_tier_valid
            CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_programs ADD COLUMN IF NOT EXISTS age_tier smallint;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'channel_programs_age_tier_valid') THEN
        ALTER TABLE channel_programs ADD CONSTRAINT channel_programs_age_tier_valid
            CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_videos ADD COLUMN IF NOT EXISTS age_limit smallint;
-- +goose StatementEnd

-- +goose StatementBegin
-- An index build that was cut short (the app quit, the machine slept) leaves the index
-- there but INVALID: never used, kept up on every write, and skipped by IF NOT EXISTS
-- below. It is dropped so that the build runs again.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
                WHERE c.relname = 'channel_programs_rated_video_idx' AND c.relnamespace = 'public'::regnamespace
                  AND NOT i.indisvalid) THEN
        DROP INDEX IF EXISTS channel_programs_rated_video_idx;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- the guide ratings of a video's programmes: only programmes that carry a rating are in
-- it, so the schedule's own writes do not pay for it
CREATE INDEX CONCURRENTLY IF NOT EXISTS channel_programs_rated_video_idx
    ON channel_programs (video_id)
    WHERE video_id IS NOT NULL AND title_snapshot ? 'rating';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_recompute_jobs (
    id         bigserial   PRIMARY KEY,
    kind       text        NOT NULL
               CHECK (kind IN ('all', 'library', 'series', 'collection', 'youtube_source',
                               'youtube_video', 'video')),
    subject    text        NOT NULL DEFAULT '',
    cursor_at  timestamptz,
    cursor_id  uuid,
    batches    integer     NOT NULL DEFAULT 0,
    attempts   integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- a home that ran this slot before the column was in it
ALTER TABLE parental_recompute_jobs ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION parental_recompute_enqueue(p_kind text, p_subject text) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
    INSERT INTO parental_recompute_jobs (kind, subject) VALUES (p_kind, COALESCE(p_subject, ''))
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The function runs as its owner, so nobody may call it but the owner: the triggers
-- below reach it through their own SECURITY DEFINER functions, and the API (which
-- owns the queue) writes the table itself. A stage's or a plugin's role cannot put a
-- job in by hand.
REVOKE EXECUTE ON FUNCTION parental_recompute_enqueue(text, text) FROM PUBLIC;
-- +goose StatementEnd

-- +goose StatementBegin
-- A video joined or left a collection, a series or a YouTube source. A video that left,
-- or whose row now names another video or container, is always rated again: the stored
-- source is not asked, since a recompute still on its way may be about to write it, and a
-- tier nothing gives any more must never stay. Joining matters only when the container
-- has a tier.
CREATE OR REPLACE FUNCTION parental_member_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    tiered boolean := false;
BEGIN
    IF TG_OP <> 'INSERT' AND OLD.video_id IS NOT NULL THEN
        PERFORM parental_recompute_enqueue('video', OLD.video_id::text);
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.video_id IS NOT NULL
       AND (TG_OP = 'INSERT' OR NEW.video_id IS DISTINCT FROM OLD.video_id) THEN
        IF TG_TABLE_NAME = 'collection_items' THEN
            SELECT age_tier IS NOT NULL INTO tiered FROM collections WHERE id = NEW.collection_id;
        ELSIF TG_TABLE_NAME = 'series_episodes' THEN
            SELECT age_tier IS NOT NULL INTO tiered FROM series WHERE id = NEW.series_id;
        ELSE
            SELECT age_tier IS NOT NULL INTO tiered FROM youtube_sources WHERE id = NEW.source_id;
        END IF;
        IF COALESCE(tiered, false) THEN
            PERFORM parental_recompute_enqueue('video', NEW.video_id::text);
        END IF;
    END IF;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER collection_items_parental
    AFTER INSERT OR DELETE OR UPDATE OF collection_id, video_id ON collection_items
    FOR EACH ROW EXECUTE FUNCTION parental_member_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER series_episodes_parental
    AFTER INSERT OR DELETE OR UPDATE OF series_id, video_id ON series_episodes
    FOR EACH ROW EXECUTE FUNCTION parental_member_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER youtube_downloads_parental
    AFTER INSERT OR DELETE OR UPDATE OF source_id, video_id ON youtube_downloads
    FOR EACH ROW EXECUTE FUNCTION parental_member_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- A new video takes its library's default or the rating it came with; a video moved to
-- another library, or given another content_rating, is rated again.
CREATE OR REPLACE FUNCTION parental_video_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF (NEW.metadata->>'content_rating') IS NULL
           AND NOT EXISTS (SELECT 1 FROM libraries WHERE id = NEW.library_id AND age_tier IS NOT NULL) THEN
            RETURN NULL;
        END IF;
    END IF;
    PERFORM parental_recompute_enqueue('video', NEW.id::text);
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER videos_parental_ins
    AFTER INSERT ON videos
    FOR EACH ROW EXECUTE FUNCTION parental_video_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER videos_parental_upd
    AFTER UPDATE OF library_id, metadata ON videos
    FOR EACH ROW
    WHEN (OLD.library_id IS DISTINCT FROM NEW.library_id
          OR (OLD.metadata->>'content_rating') IS DISTINCT FROM (NEW.metadata->>'content_rating'))
    EXECUTE FUNCTION parental_video_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- A YouTube match made, changed or dropped (external_id is the bare id or "youtube:<id>",
-- as slot 0164). The video a match was dropped from, or pointed away from, is always
-- rated again, whatever its stored source says; a new match matters when its YouTube
-- video has an age limit.
CREATE OR REPLACE FUNCTION parental_youtube_match_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF TG_OP <> 'INSERT' AND OLD.provider = 'youtube' THEN
        PERFORM parental_recompute_enqueue('video', OLD.video_id::text);
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.provider = 'youtube'
       AND (TG_OP = 'INSERT' OR OLD.provider <> 'youtube'
            OR NEW.video_id IS DISTINCT FROM OLD.video_id) THEN
        IF EXISTS (SELECT 1 FROM youtube_videos y
                    WHERE y.youtube_id = CASE WHEN NEW.external_id LIKE 'youtube:%'
                                              THEN substr(NEW.external_id, 9)
                                              ELSE NEW.external_id END
                      AND y.age_limit > 0) THEN
            PERFORM parental_recompute_enqueue('video', NEW.video_id::text);
        END IF;
    END IF;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- (an earlier form of this slot also fired it on every UPDATE; replacing it narrows it)
CREATE OR REPLACE TRIGGER media_metadata_enrichment_parental
    AFTER INSERT OR DELETE ON media_metadata_enrichment
    FOR EACH ROW EXECUTE FUNCTION parental_youtube_match_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- An UPDATE asks only when what the tier is read from changed: the video, the provider,
-- the YouTube id or the confidence that picks the best match. A refresh that writes a
-- match back as it was (an enrichment pass over a whole library) queues nothing.
CREATE OR REPLACE TRIGGER media_metadata_enrichment_parental_upd
    AFTER UPDATE OF video_id, provider, external_id, confidence ON media_metadata_enrichment
    FOR EACH ROW
    WHEN (OLD.video_id IS DISTINCT FROM NEW.video_id
          OR OLD.provider IS DISTINCT FROM NEW.provider
          OR OLD.external_id IS DISTINCT FROM NEW.external_id
          OR OLD.confidence IS DISTINCT FROM NEW.confidence)
    EXECUTE FUNCTION parental_youtube_match_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION parental_youtube_age_limit_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF TG_OP = 'DELETE' THEN  -- the videos matched to it lose its age limit
        PERFORM parental_recompute_enqueue('youtube_video', OLD.youtube_id);
    ELSE
        PERFORM parental_recompute_enqueue('youtube_video', NEW.youtube_id);
    END IF;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER youtube_videos_parental_ins
    AFTER INSERT ON youtube_videos
    FOR EACH ROW WHEN (NEW.age_limit > 0)
    EXECUTE FUNCTION parental_youtube_age_limit_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER youtube_videos_parental_upd
    AFTER UPDATE OF age_limit ON youtube_videos
    FOR EACH ROW WHEN (OLD.age_limit IS DISTINCT FROM NEW.age_limit)
    EXECUTE FUNCTION parental_youtube_age_limit_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER youtube_videos_parental_del
    AFTER DELETE ON youtube_videos
    FOR EACH ROW WHEN (OLD.age_limit > 0)
    EXECUTE FUNCTION parental_youtube_age_limit_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- A kept Plex item's rating or its video changed, or the item went.
CREATE OR REPLACE FUNCTION parental_plex_item_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    old_video  uuid;
    new_video  uuid;
    old_rating text := '';
    new_rating text := '';
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_video := OLD.video_id;
        old_rating := COALESCE(OLD.detail->>'contentRating', '');
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_video := NEW.video_id;
        new_rating := COALESCE(NEW.detail->>'contentRating', '');
    END IF;
    IF old_video IS NOT DISTINCT FROM new_video AND old_rating = new_rating THEN
        RETURN NULL;
    END IF;
    IF old_video IS NOT NULL AND old_rating <> '' THEN
        PERFORM parental_recompute_enqueue('video', old_video::text);
    END IF;
    IF new_video IS NOT NULL AND new_rating <> ''
       AND (new_video IS DISTINCT FROM old_video OR old_rating = '') THEN
        PERFORM parental_recompute_enqueue('video', new_video::text);
    END IF;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER plex_items_parental
    AFTER INSERT OR DELETE OR UPDATE OF video_id, detail ON plex_items
    FOR EACH ROW EXECUTE FUNCTION parental_plex_item_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- A guide programme's rating or its video changed, or the programme left the schedule.
-- A programme with no rating, and a write that changes neither, queue nothing: the
-- schedule's own churn costs one comparison a row.
CREATE OR REPLACE FUNCTION parental_program_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    old_video  uuid;
    new_video  uuid;
    old_rating text := '';
    new_rating text := '';
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_video := OLD.video_id;
        old_rating := COALESCE(OLD.title_snapshot->>'rating', '');
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_video := NEW.video_id;
        new_rating := COALESCE(NEW.title_snapshot->>'rating', '');
    END IF;
    IF old_video IS NOT DISTINCT FROM new_video AND old_rating = new_rating THEN
        RETURN NULL;
    END IF;
    IF old_video IS NOT NULL AND old_rating <> '' THEN
        PERFORM parental_recompute_enqueue('video', old_video::text);
    END IF;
    IF new_video IS NOT NULL AND new_rating <> ''
       AND (new_video IS DISTINCT FROM old_video OR old_rating = '') THEN
        PERFORM parental_recompute_enqueue('video', new_video::text);
    END IF;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER channel_programs_parental
    AFTER INSERT OR DELETE OR UPDATE OF video_id, title_snapshot ON channel_programs
    FOR EACH ROW EXECUTE FUNCTION parental_program_changed();
-- +goose StatementEnd

-- +goose StatementBegin
-- trigger functions are never called by hand
REVOKE EXECUTE ON FUNCTION parental_member_changed(), parental_video_changed(),
    parental_youtube_match_changed(), parental_youtube_age_limit_changed(),
    parental_plex_item_changed(), parental_program_changed() FROM PUBLIC;
-- +goose StatementEnd

-- +goose StatementBegin
-- the one-off recompute of every video (not put in again while one waits)
INSERT INTO parental_recompute_jobs (kind)
SELECT 'all' WHERE NOT EXISTS (SELECT 1 FROM parental_recompute_jobs WHERE kind = 'all');
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TRIGGER IF EXISTS channel_programs_parental ON channel_programs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS plex_items_parental ON plex_items;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_program_changed();
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_plex_item_changed();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS youtube_videos_parental_del ON youtube_videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS youtube_videos_parental_upd ON youtube_videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS youtube_videos_parental_ins ON youtube_videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS media_metadata_enrichment_parental_upd ON media_metadata_enrichment;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS media_metadata_enrichment_parental ON media_metadata_enrichment;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS videos_parental_upd ON videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS videos_parental_ins ON videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS youtube_downloads_parental ON youtube_downloads;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS series_episodes_parental ON series_episodes;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS collection_items_parental ON collection_items;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_youtube_age_limit_changed();
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_youtube_match_changed();
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_video_changed();
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_member_changed();
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_recompute_enqueue(text, text);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_recompute_jobs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channel_programs_rated_video_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_videos DROP COLUMN IF EXISTS age_limit;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_programs DROP CONSTRAINT IF EXISTS channel_programs_age_tier_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_programs DROP COLUMN IF EXISTS age_tier;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_sources DROP CONSTRAINT IF EXISTS youtube_sources_age_tier_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_sources DROP COLUMN IF EXISTS age_tier;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_age_tier_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels DROP COLUMN IF EXISTS age_tier;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections DROP CONSTRAINT IF EXISTS collections_age_tier_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections DROP COLUMN IF EXISTS age_tier;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE series DROP CONSTRAINT IF EXISTS series_age_tier_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE series DROP COLUMN IF EXISTS age_tier;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries DROP CONSTRAINT IF EXISTS libraries_age_tier_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries DROP COLUMN IF EXISTS age_tier;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS videos_library_age_tier_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_age_tier_admin_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS age_tier_admin;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_age_tier_source_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS age_tier_source;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_age_tier_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS age_tier;
-- +goose StatementEnd
