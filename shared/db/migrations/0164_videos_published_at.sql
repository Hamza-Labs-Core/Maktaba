-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0164: a video's publish date is when it came out, not when its file was
-- written. videos.mtime is the download's time for most files (a 2015 episode
-- downloaded this year counted in this year), so the date is the video's YouTube
-- upload date where Maktaba knows it:
--
--   videos.published_at  the upload date (youtube_videos.upload_date, midnight UTC) of
--                        the video's best YouTube match: its media_metadata_enrichment
--                        row with provider 'youtube' of the highest confidence (the
--                        newest, then the id, on a tie). NULL when it has none, or
--                        that match has no known date.
--
-- Readers take COALESCE(published_at, mtime): a library's years, the year filter, the
-- sort by publish date and the cards' published_at. "Recently added" stays created_at.
--
-- The writers of a match or an upload date (the pipeline's link, youtube_sync,
-- exact_dates and import_mediaintel; the API's YouTube import) are kept in step by the
-- triggers below, so none of them has to know. external_id is the bare YouTube id, or
-- "youtube:<id>" from the API's import.
--
-- The backfill is one UPDATE of the videos that have a YouTube match (about 140k of
-- 150k on the largest home), each an index lookup through
-- media_metadata_enrichment_video_idx. NO TRANSACTION for the indexes (CONCURRENTLY:
-- the table is large), as slot 0159. Additive; a previous release ignores the column.
--
ALTER TABLE videos ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
-- The rule, in one place: the backfill and the triggers both read it.
CREATE OR REPLACE FUNCTION video_published_at(p_video uuid) RETURNS timestamptz
LANGUAGE sql STABLE AS $f$
    SELECT y.upload_date::timestamp AT TIME ZONE 'UTC'
      FROM media_metadata_enrichment m
      LEFT JOIN youtube_videos y ON y.youtube_id = CASE WHEN m.external_id LIKE 'youtube:%'
                                                        THEN substr(m.external_id, 9)
                                                        ELSE m.external_id END
     WHERE m.video_id = p_video AND m.provider = 'youtube'
     ORDER BY m.confidence DESC, m.created_at DESC, m.id
     LIMIT 1
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Writes the video's published_at when it changed (an unchanged row is not rewritten).
CREATE OR REPLACE FUNCTION video_published_at_refresh(p_video uuid) RETURNS void
LANGUAGE plpgsql AS $f$
DECLARE
    v_at timestamptz := video_published_at(p_video);
BEGIN
    UPDATE videos SET published_at = v_at WHERE id = p_video AND published_at IS DISTINCT FROM v_at;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A YouTube match added, changed or removed: its video, before and after.
CREATE OR REPLACE FUNCTION media_metadata_enrichment_published_at() RETURNS trigger
LANGUAGE plpgsql AS $f$
BEGIN
    IF TG_OP <> 'INSERT' AND OLD.provider = 'youtube' THEN
        PERFORM video_published_at_refresh(OLD.video_id);
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.provider = 'youtube'
       AND (TG_OP = 'INSERT' OR NEW.video_id IS DISTINCT FROM OLD.video_id OR OLD.provider <> 'youtube') THEN
        PERFORM video_published_at_refresh(NEW.video_id);
    END IF;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER media_metadata_enrichment_published_at
    AFTER INSERT OR DELETE OR UPDATE OF video_id, provider, external_id, confidence, created_at
    ON media_metadata_enrichment FOR EACH ROW EXECUTE FUNCTION media_metadata_enrichment_published_at();
-- +goose StatementEnd

-- +goose StatementBegin
-- An upload date learned, corrected or gone: the videos matched to that YouTube video.
CREATE OR REPLACE FUNCTION youtube_videos_published_at() RETURNS trigger
LANGUAGE plpgsql AS $f$
DECLARE
    v_id text;
BEGIN
    FOR v_id IN SELECT DISTINCT x FROM unnest(ARRAY[
            CASE WHEN TG_OP <> 'INSERT' THEN OLD.youtube_id END,
            CASE WHEN TG_OP <> 'DELETE' THEN NEW.youtube_id END]) AS x WHERE x IS NOT NULL
    LOOP
        PERFORM video_published_at_refresh(m.video_id)
           FROM media_metadata_enrichment m
          WHERE m.provider = 'youtube' AND m.external_id IN (v_id, 'youtube:' || v_id);
    END LOOP;
    RETURN NULL;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- youtube_sync upserts a channel's whole list: a row whose date did not change fires
-- nothing.
CREATE OR REPLACE TRIGGER youtube_videos_published_at_ins
    AFTER INSERT ON youtube_videos FOR EACH ROW
    WHEN (NEW.upload_date IS NOT NULL)
    EXECUTE FUNCTION youtube_videos_published_at();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER youtube_videos_published_at_upd
    AFTER UPDATE OF upload_date, youtube_id ON youtube_videos FOR EACH ROW
    WHEN (OLD.upload_date IS DISTINCT FROM NEW.upload_date OR OLD.youtube_id <> NEW.youtube_id)
    EXECUTE FUNCTION youtube_videos_published_at();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER youtube_videos_published_at_del
    AFTER DELETE ON youtube_videos FOR EACH ROW
    WHEN (OLD.upload_date IS NOT NULL)
    EXECUTE FUNCTION youtube_videos_published_at();
-- +goose StatementEnd

-- +goose StatementBegin
-- The triggers' lookup of a YouTube video's matches.
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_metadata_enrichment_youtube_idx
    ON media_metadata_enrichment (external_id) WHERE provider = 'youtube';
-- +goose StatementEnd

-- +goose StatementBegin
-- The backfill: every video with a YouTube match (the rest stay NULL).
UPDATE videos v SET published_at = video_published_at(v.id)
 WHERE v.id IN (SELECT m.video_id FROM media_metadata_enrichment m WHERE m.provider = 'youtube')
   AND v.published_at IS DISTINCT FROM video_published_at(v.id);
-- +goose StatementEnd

-- +goose StatementBegin
-- A library's grid by publish date and its years row (slot 0159's (library_id, mtime)
-- index, on the date the readers use).
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_published_idx
    ON videos (library_id, (COALESCE(published_at, mtime)) DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_published_idx;
DROP INDEX IF EXISTS media_metadata_enrichment_youtube_idx;
DROP TRIGGER IF EXISTS youtube_videos_published_at_del ON youtube_videos;
DROP TRIGGER IF EXISTS youtube_videos_published_at_upd ON youtube_videos;
DROP TRIGGER IF EXISTS youtube_videos_published_at_ins ON youtube_videos;
DROP FUNCTION IF EXISTS youtube_videos_published_at();
DROP TRIGGER IF EXISTS media_metadata_enrichment_published_at ON media_metadata_enrichment;
DROP FUNCTION IF EXISTS media_metadata_enrichment_published_at();
DROP FUNCTION IF EXISTS video_published_at_refresh(uuid);
DROP FUNCTION IF EXISTS video_published_at(uuid);
ALTER TABLE videos DROP COLUMN IF EXISTS published_at;
-- +goose StatementEnd
