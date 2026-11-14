-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0227 (Epic 50, story 50.23) - publishable sources: what a published channel may air.
--
-- An item is publishable for one of four reasons, all decided on the home by its admins:
--
--   own                the admin marked it (or its folder, series or collection) as their
--                      own recordings                         videos.publishable_reason
--   cleared            the admin marked this one item with a proof link
--                                                             videos.publishable_reason
--   cc_by              YouTube says the video is Creative Commons Attribution: the
--                      intake reads yt-dlp's `license` (and the Data API's
--                      status.license on a refresh)             youtube_videos.license
--   source_permission  a source the item comes from allows sharing: a YouTube channel,
--                      a playlist, a speaker (a speaker named on the item, or a reciter's
--                      library) or a label                       publishable_sources
--
--   publishable_sources   one row per permission an admin recorded: the kind and its
--                         reference, the proof link (and an optional screenshot file), a
--                         note, a credit line, whether the source asks for non-commercial
--                         use, who recorded it and when; a withdrawal sets revoked_at and
--                         its reason (the row stays, as the record)
--   videos.publishable_reason / publishable_proof / publishable_by / publishable_at
--                         the admin's own mark on an item (own or cleared) and its proof
--   youtube_videos.license
--                         cc_by or standard (NULL: not known yet)
--   broadcast_channels.geo_mode
--                         how a channel's countries read: allow (it plays only there) or
--                         deny (it plays everywhere but there)
--
-- The functions below are the one definition of the rule; the scheduler (pipeline), the
-- channel's player (streaming) and the Broadcast page (API) all call them:
--
--   video_youtube_id(video)         the YouTube video an item was linked to
--   video_permission_source(video)  the first standing permission that covers it
--   video_publishable_reason(video) own, cleared, cc_by, source_permission or NULL
--   video_attribution(video)        what a guide entry shows: the CC BY attribution or a
--                                   source's credit line (NULL: nothing to show)
--   channel_publishable_only(chan)  true while the channel is published and the home keeps
--                                   app_settings `broadcast.publishable_only` on (default)
--
-- The three a plugin's statement calls (video_publishable_reason, video_attribution and
-- channel_publishable_only) are SECURITY DEFINER: they answer for one item or channel, and
-- a plugin's role needs no grant on publishable_sources or the tables they read.
--
-- Nothing here leaves the home: the cloud learns only a programme's attribution, in the
-- guide it is sent.
--
-- Additive: new table, new nullable columns, new functions. An older release never reads them.
--
CREATE TABLE IF NOT EXISTS publishable_sources (
    id                uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    kind              text         NOT NULL
        CONSTRAINT publishable_sources_kind_valid CHECK (kind IN ('channel', 'playlist', 'speaker', 'label')),
    ref               text         NOT NULL
        CONSTRAINT publishable_sources_ref_set CHECK (length(btrim(ref)) > 0),
    label             text         NOT NULL DEFAULT '',
    proof_url         text         NOT NULL
        CONSTRAINT publishable_sources_proof_url_valid CHECK (proof_url ~* '^https?://'),
    proof_file        text,
    note              text         NOT NULL DEFAULT '',
    credit            text         NOT NULL DEFAULT '',
    non_commercial    boolean      NOT NULL DEFAULT false,
    recorded_by       text         NOT NULL,
    recorded_by_user  uuid         REFERENCES users(id) ON DELETE SET NULL,
    recorded_at       timestamptz  NOT NULL DEFAULT now(),
    revoked_at        timestamptz,
    revoked_reason    text
);
-- +goose StatementEnd

-- +goose StatementBegin
-- one standing permission per source (a withdrawn one stays as the record). Built
-- concurrently, so the slot runs outside a transaction; every statement here can run again.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS publishable_sources_standing_idx
    ON publishable_sources (kind, lower(ref)) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS publishable_reason text
    CONSTRAINT videos_publishable_reason_valid CHECK (publishable_reason IN ('own', 'cleared'));
ALTER TABLE videos ADD COLUMN IF NOT EXISTS publishable_proof jsonb;
ALTER TABLE videos ADD COLUMN IF NOT EXISTS publishable_by text;
ALTER TABLE videos ADD COLUMN IF NOT EXISTS publishable_at timestamptz;
ALTER TABLE youtube_videos ADD COLUMN IF NOT EXISTS license text
    CONSTRAINT youtube_videos_license_valid CHECK (license IN ('cc_by', 'standard'));
ALTER TABLE broadcast_channels ADD COLUMN IF NOT EXISTS geo_mode text NOT NULL DEFAULT 'allow'
    CONSTRAINT broadcast_channels_geo_mode_valid CHECK (geo_mode IN ('allow', 'deny'));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION video_youtube_id(vid uuid) RETURNS text
LANGUAGE sql STABLE AS $f$
    SELECT CASE WHEN m.external_id LIKE 'youtube:%' THEN substr(m.external_id, 9) ELSE m.external_id END
      FROM media_metadata_enrichment m
     WHERE m.video_id = vid AND m.provider = 'youtube'
     ORDER BY m.confidence DESC, m.created_at DESC, m.id
     LIMIT 1
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION video_permission_source(vid uuid) RETURNS uuid
LANGUAGE sql STABLE AS $f$
    WITH yt AS (SELECT video_youtube_id(vid) AS yid)
    SELECT p.id
      FROM publishable_sources p, yt
     WHERE p.revoked_at IS NULL
       AND (
            (p.kind = 'channel' AND EXISTS (
                SELECT 1 FROM youtube_videos y WHERE y.youtube_id = yt.yid AND y.channel_id = p.ref))
         OR (p.kind = 'playlist' AND EXISTS (
                SELECT 1 FROM youtube_playlist_items pi WHERE pi.playlist_id = p.ref AND pi.youtube_id = yt.yid))
         OR (p.kind = 'speaker' AND (
                EXISTS (SELECT 1 FROM speakers s WHERE s.video_id = vid AND lower(s.name) = lower(p.ref))
             OR EXISTS (SELECT 1 FROM videos v JOIN libraries l ON l.id = v.library_id
                         WHERE v.id = vid AND lower(l.name) = lower(p.ref))))
         OR (p.kind = 'label' AND EXISTS (
                SELECT 1 FROM videos v WHERE v.id = vid AND lower(v.metadata->>'label') = lower(p.ref)))
       )
     ORDER BY p.recorded_at, p.id
     LIMIT 1
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION video_publishable_reason(vid uuid) RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $f$
    SELECT COALESCE(
        (SELECT v.publishable_reason FROM videos v WHERE v.id = vid),
        (SELECT 'cc_by' FROM youtube_videos y WHERE y.youtube_id = video_youtube_id(vid) AND y.license = 'cc_by'),
        (SELECT 'source_permission' WHERE video_permission_source(vid) IS NOT NULL)
    )
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION video_attribution(vid uuid) RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $f$
    SELECT COALESCE(
        (SELECT jsonb_build_object(
                    'kind', 'cc_by', 'licence', 'CC BY', 'title', y.title,
                    'author', COALESCE(c.title, ''),
                    'url', 'https://www.youtube.com/watch?v=' || y.youtube_id)
           FROM youtube_videos y LEFT JOIN youtube_channels c ON c.channel_id = y.channel_id
          WHERE y.youtube_id = video_youtube_id(vid) AND y.license = 'cc_by'
            AND NOT EXISTS (SELECT 1 FROM videos v WHERE v.id = vid AND v.publishable_reason IS NOT NULL)),
        (SELECT jsonb_build_object('kind', 'credit', 'credit', p.credit)
           FROM publishable_sources p
          WHERE p.id = video_permission_source(vid) AND p.credit <> ''
            AND NOT EXISTS (SELECT 1 FROM videos v WHERE v.id = vid AND v.publishable_reason IS NOT NULL))
    )
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION channel_publishable_only(chan uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $f$
    SELECT EXISTS (SELECT 1 FROM broadcast_channels b WHERE b.channel_id = chan AND b.state IN ('live', 'offline'))
       AND NOT EXISTS (SELECT 1 FROM app_settings s WHERE s.key = 'broadcast.publishable_only'
                                                       AND s.value = 'false'::jsonb)
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS channel_publishable_only(uuid);
DROP FUNCTION IF EXISTS video_attribution(uuid);
DROP FUNCTION IF EXISTS video_publishable_reason(uuid);
DROP FUNCTION IF EXISTS video_permission_source(uuid);
DROP FUNCTION IF EXISTS video_youtube_id(uuid);
ALTER TABLE broadcast_channels DROP COLUMN IF EXISTS geo_mode;
ALTER TABLE youtube_videos DROP COLUMN IF EXISTS license;
ALTER TABLE videos DROP COLUMN IF EXISTS publishable_at;
ALTER TABLE videos DROP COLUMN IF EXISTS publishable_by;
ALTER TABLE videos DROP COLUMN IF EXISTS publishable_proof;
ALTER TABLE videos DROP COLUMN IF EXISTS publishable_reason;
DROP INDEX IF EXISTS publishable_sources_standing_idx;
DROP TABLE IF EXISTS publishable_sources;
DELETE FROM app_settings WHERE key = 'broadcast.publishable_only';
-- +goose StatementEnd
