-- +goose Up
-- +goose StatementBegin
--
-- Slot 0228 (Epic 50, story 50.24) - takedowns: what Maktaba Cloud's signed instruction took
-- off a published channel after a copyright notice.
--
--   broadcast_takedowns   one row per instruction the home applied: the item (a video) or the
--                         source (a YouTube channel or playlist, a speaker, a label) it names,
--                         the notice, when it was applied and, after a counter-notice or a
--                         withdrawn notice, when Maktaba restored it
--
-- video_taken_down(video) is true while a takedown stands on the item or on a source it comes
-- from, and video_publishable_reason() (slot 0227's one rule) answers NULL for it whatever its
-- reason was (an own mark, a cleared mark, CC BY, a permission): the scheduler leaves it out, the
-- channel's player slates a slot already scheduled, and the Broadcast page's share drops it.
-- Nothing else of the library changes, and nothing here leaves the home.
--
-- Additive: a new table and function, slot 0227's function re-created with one more check.
--
CREATE TABLE IF NOT EXISTS broadcast_takedowns (
    id           text         PRIMARY KEY,
    notice_id    text         NOT NULL DEFAULT '',
    scope        text         NOT NULL
        CONSTRAINT broadcast_takedowns_scope_valid CHECK (scope IN ('item', 'source')),
    video_id     uuid         REFERENCES videos(id) ON DELETE SET NULL,
    source_kind  text         NOT NULL DEFAULT ''
        CONSTRAINT broadcast_takedowns_source_kind_valid CHECK (source_kind IN ('', 'channel', 'playlist', 'speaker', 'label')),
    source_ref   text         NOT NULL DEFAULT '',
    channel_id   uuid         REFERENCES channels(id) ON DELETE SET NULL,
    programme    text         NOT NULL DEFAULT '',
    applied_at   timestamptz  NOT NULL DEFAULT now(),
    restored_at  timestamptz,
    restored_by  text         NOT NULL DEFAULT ''
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS broadcast_takedowns_video_idx ON broadcast_takedowns (video_id) WHERE restored_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION video_taken_down(vid uuid) RETURNS boolean
LANGUAGE sql STABLE AS $f$
    WITH yt AS (SELECT video_youtube_id(vid) AS yid)
    SELECT EXISTS (
        SELECT 1 FROM broadcast_takedowns t, yt
         WHERE t.restored_at IS NULL
           AND ((t.scope = 'item' AND t.video_id = vid)
             OR (t.scope = 'source' AND (
                    (t.source_kind = 'channel' AND EXISTS (
                        SELECT 1 FROM youtube_videos y WHERE y.youtube_id = yt.yid AND y.channel_id = t.source_ref))
                 OR (t.source_kind = 'playlist' AND EXISTS (
                        SELECT 1 FROM youtube_playlist_items pi WHERE pi.playlist_id = t.source_ref AND pi.youtube_id = yt.yid))
                 OR (t.source_kind = 'speaker' AND (
                        EXISTS (SELECT 1 FROM speakers s WHERE s.video_id = vid AND lower(s.name) = lower(t.source_ref))
                     OR EXISTS (SELECT 1 FROM videos v JOIN libraries l ON l.id = v.library_id
                                 WHERE v.id = vid AND lower(l.name) = lower(t.source_ref))))
                 OR (t.source_kind = 'label' AND EXISTS (
                        SELECT 1 FROM videos v WHERE v.id = vid AND lower(v.metadata->>'label') = lower(t.source_ref))))))
    )
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION video_publishable_reason(vid uuid) RETURNS text
LANGUAGE sql STABLE AS $f$
    SELECT CASE WHEN video_taken_down(vid) THEN NULL ELSE COALESCE(
        (SELECT v.publishable_reason FROM videos v WHERE v.id = vid),
        (SELECT 'cc_by' FROM youtube_videos y WHERE y.youtube_id = video_youtube_id(vid) AND y.license = 'cc_by'),
        (SELECT 'source_permission' WHERE video_permission_source(vid) IS NOT NULL)
    ) END
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION video_publishable_reason(vid uuid) RETURNS text
LANGUAGE sql STABLE AS $f$
    SELECT COALESCE(
        (SELECT v.publishable_reason FROM videos v WHERE v.id = vid),
        (SELECT 'cc_by' FROM youtube_videos y WHERE y.youtube_id = video_youtube_id(vid) AND y.license = 'cc_by'),
        (SELECT 'source_permission' WHERE video_permission_source(vid) IS NOT NULL)
    )
$f$;
DROP FUNCTION IF EXISTS video_taken_down(uuid);
DROP INDEX IF EXISTS broadcast_takedowns_video_idx;
DROP TABLE IF EXISTS broadcast_takedowns;
-- +goose StatementEnd
