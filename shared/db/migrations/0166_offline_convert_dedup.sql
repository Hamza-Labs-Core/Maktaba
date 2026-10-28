-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0166 (Epic 47, stories 47.1-47.3) - keep libraries offline, in MP4 and without
-- duplicates. Shared base of three queue pipelines (convert, dedup, YouTube refresh).
--
--   file_actions        one row per file operation: the audit, the crash-recovery journal
--                       and the undo list (planned -> staged -> verified -> swapped -> done,
--                       or rolled_back / failed). A removed file goes to
--                       <library root>/.maktaba/trash/<kind>/<yyyy-mm-dd>/<relative path>
--                       until purge_after (library settings trash.days, default 30)
--   video_copies        extra paths of a byte-identical file the scanner found (47.5)
--   duplicate_groups    groups dedup_scan found (tier exact | same_video), with the
--     duplicate_members   members; members_key (sorted member ids) remembers a dismissal
--   youtube_sources     channels and playlists a library follows; mode off |
--                       new_and_backfill | new_only; added_at splits new from back catalogue.
--                       Backfilled from youtube_channels with mode 'off'
--   youtube_downloads   what a library wants from YouTube, and what came of it
--   videos.full_hash    full-file BLAKE3, computed on demand for exact candidates
--   videos.superseded_by  a merged duplicate's keeper (state 'superseded')
--   subject_kind        gains 'dup_group' (a duplicate_groups row; library_id = its library)
--   worker_pools        'convert' (size 1); job_stages convert_mp4 (home only), dedup_scan,
--                       dedup_apply, trash_purge, youtube_fetch, youtube_backfill. Every
--                       one but trash_purge is off ('not_configured') until its story ships.
--
-- Additive: no existing row changes but the youtube_sources backfill (new rows only) and
-- the processing_jobs subject CHECK, which only widens.
--
CREATE TABLE IF NOT EXISTS file_actions (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id   UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    video_id     UUID        REFERENCES videos(id) ON DELETE SET NULL,
    job_id       BIGINT,
    kind         TEXT        NOT NULL
                 CHECK (kind IN ('convert', 'dedup', 'youtube_fetch', 'purge', 'restore')),
    state        TEXT        NOT NULL DEFAULT 'planned'
                 CHECK (state IN ('planned', 'staged', 'verified', 'swapped', 'done',
                                  'rolled_back', 'failed')),
    old_path     TEXT,
    new_path     TEXT,
    trash_path   TEXT,
    old_hash     TEXT,
    new_hash     TEXT,
    old_size     BIGINT,
    new_size     BIGINT,
    -- what the check measured: durations, streams, decode errors, frames
    verify       JSONB,
    error        TEXT,
    purge_after  TIMESTAMPTZ,
    purged_at    TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS file_actions_open_idx
    ON file_actions (state, updated_at)
    WHERE state IN ('planned', 'staged', 'verified', 'swapped');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS file_actions_purge_idx
    ON file_actions (library_id, purge_after)
    WHERE trash_path IS NOT NULL AND purged_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS file_actions_library_idx
    ON file_actions (library_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS file_actions_video_idx
    ON file_actions (video_id) WHERE video_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS video_copies (
    video_id    UUID        NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    path        TEXT        NOT NULL,
    size_bytes  BIGINT      NOT NULL,
    mtime       TIMESTAMPTZ,
    seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (video_id, path)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_copies_path_idx ON video_copies (path);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS duplicate_groups (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- NULL: a group across libraries
    library_id       UUID        REFERENCES libraries(id) ON DELETE CASCADE,
    tier             TEXT        NOT NULL CHECK (tier IN ('exact', 'same_video')),
    status           TEXT        NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open', 'applied', 'dismissed', 'failed')),
    keeper_video_id  UUID        REFERENCES videos(id) ON DELETE SET NULL,
    -- the hash of the sorted member ids: a dismissed group does not come back
    members_key      TEXT        NOT NULL,
    reason           TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_by       TEXT,
    decided_at       TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS duplicate_groups_library_idx
    ON duplicate_groups (library_id, status, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS duplicate_groups_members_key_idx
    ON duplicate_groups (members_key);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS duplicate_members (
    group_id  UUID    NOT NULL REFERENCES duplicate_groups(id) ON DELETE CASCADE,
    video_id  UUID    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    path      TEXT    NOT NULL,
    is_copy   BOOLEAN NOT NULL DEFAULT false,
    score     JSONB,
    PRIMARY KEY (group_id, video_id, path)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS duplicate_members_video_idx
    ON duplicate_members (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_sources (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id       UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind             TEXT        NOT NULL CHECK (kind IN ('channel', 'playlist')),
    url              TEXT        NOT NULL,
    external_id      TEXT        NOT NULL,
    -- relative to the library's first root; NULL: the source's sanitised title
    folder           TEXT,
    mode             TEXT        NOT NULL DEFAULT 'new_and_backfill'
                     CHECK (mode IN ('off', 'new_and_backfill', 'new_only')),
    -- uploads after this are new; older ones are back catalogue
    added_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- max_height, sub_langs, audio_only, cookies_from_browser
    options          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    status           TEXT,
    error            TEXT,
    last_checked_at  TIMESTAMPTZ,
    last_fetched_at  TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (library_id, kind, external_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_downloads (
    library_id     UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    youtube_id     TEXT        NOT NULL,
    source_id      UUID        REFERENCES youtube_sources(id) ON DELETE SET NULL,
    state          TEXT        NOT NULL DEFAULT 'wanted'
                   CHECK (state IN ('wanted', 'downloading', 'done', 'skipped', 'gone',
                                    'failed')),
    -- true: back catalogue (uploaded before the source's added_at)
    backfill       BOOLEAN     NOT NULL DEFAULT false,
    -- members_only, private, age_gate, geo, live, removed, too_old ...
    reason         TEXT,
    video_id       UUID        REFERENCES videos(id) ON DELETE SET NULL,
    attempts       INT         NOT NULL DEFAULT 0,
    failure_class  TEXT,
    error          TEXT,
    not_before     TIMESTAMPTZ,
    bytes          BIGINT,
    upload_date    DATE,
    requested_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    done_at        TIMESTAMPTZ,
    PRIMARY KEY (library_id, youtube_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS youtube_downloads_wanted_idx
    ON youtube_downloads (library_id, backfill, upload_date)
    WHERE state = 'wanted';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS youtube_downloads_source_idx
    ON youtube_downloads (source_id) WHERE source_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS full_hash TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS superseded_by UUID
    REFERENCES videos(id) ON DELETE SET NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_superseded_by_idx
    ON videos (superseded_by) WHERE superseded_by IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_full_hash_idx
    ON videos (library_id, full_hash) WHERE full_hash IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO worker_pools (pool, label, scope, size, default_size)
VALUES ('convert', 'Convert to MP4', 'global', 1, 1)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('convert_mp4', 'Convert to MP4', ARRAY['video']::text[], 'convert', 'media', 14, 1, 0, 0, 2, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, true, 'auto', 'home', false, 'not_configured'),
    ('dedup_scan', 'Find duplicates', ARRAY['library']::text[], 'nas_read', 'media', 15, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'any', false, 'not_configured'),
    ('dedup_apply', 'Merge duplicates', ARRAY['dup_group']::text[], 'nas_write', 'media', 16, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, true, 'auto', 'any', false, 'not_configured'),
    ('trash_purge', 'Empty file trash', ARRAY['library']::text[], 'nas_write', 'media', 17, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, true, 'auto', 'any', true, NULL),
    ('youtube_fetch', 'YouTube downloads', ARRAY['library']::text[], 'network_yt', 'sources', 2, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, true, 'auto', 'any', false, 'not_configured'),
    ('youtube_backfill', 'YouTube back catalogue', ARRAY['library']::text[], 'network_yt', 'sources', 3, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, true, 'auto', 'any', false, 'not_configured')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_subject_chk;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs
    ADD CONSTRAINT processing_jobs_subject_chk CHECK (
        subject_id IS NOT NULL AND (
            (subject_kind = 'video'   AND video_id   = subject_id)
         OR (subject_kind = 'library' AND library_id = subject_id AND video_id IS NULL)
         OR (subject_kind = 'series'  AND series_id  = subject_id AND video_id IS NULL)
         OR (subject_kind = 'season'  AND season_id  = subject_id AND video_id IS NULL)
         OR (subject_kind = 'catalog'
             AND subject_id = '00000000-0000-0000-0000-00000000ca7a'::uuid
             AND video_id IS NULL)
         OR (subject_kind = 'dup_group'
             AND video_id IS NULL AND series_id IS NULL AND season_id IS NULL)
        )
    ) NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs VALIDATE CONSTRAINT processing_jobs_subject_chk;
-- +goose StatementEnd

-- +goose StatementBegin
-- the enqueue knows the new subject: a duplicate group, in its library (may be NULL)
CREATE OR REPLACE FUNCTION processing_jobs_enqueue(
    p_subject_kind   TEXT,
    p_subject_id     UUID,
    p_stage          TEXT,
    p_priority       INT     DEFAULT NULL,
    p_force          BOOLEAN DEFAULT false,
    p_force_cascade  BOOLEAN DEFAULT false,
    p_reason         JSONB   DEFAULT '{}'::jsonb,
    p_triggered_by   BIGINT  DEFAULT NULL,
    p_payload        JSONB   DEFAULT NULL,
    p_max_attempts   INT     DEFAULT NULL
) RETURNS TABLE (job_id BIGINT, outcome TEXT)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    s            job_stages%ROWTYPE;
    v_library    UUID;
    v_video      UUID;
    v_series     UUID;
    v_season     UUID;
    v_entry      JSONB;
    v_from_stage TEXT;
    v_id         BIGINT;
    v_outcome    TEXT;
    v_catalog    CONSTANT UUID := '00000000-0000-0000-0000-00000000ca7a';
BEGIN
    SELECT * INTO s FROM job_stages WHERE stage = p_stage;
    IF NOT FOUND THEN
        RETURN QUERY SELECT NULL::BIGINT, 'unknown_stage'::TEXT;
        RETURN;
    END IF;
    IF NOT s.enabled THEN
        RETURN QUERY SELECT NULL::BIGINT, 'disabled'::TEXT;
        RETURN;
    END IF;
    IF NOT (p_subject_kind = ANY (s.subject_kinds)) THEN
        RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
        RETURN;
    END IF;

    IF p_subject_kind = 'video' THEN
        SELECT v.library_id INTO v_library FROM videos v WHERE v.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_video := p_subject_id;
    ELSIF p_subject_kind = 'season' THEN
        SELECT ss.library_id INTO v_library FROM series_seasons ss WHERE ss.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_season := p_subject_id;
    ELSIF p_subject_kind = 'series' THEN
        SELECT sr.library_id INTO v_library FROM series sr WHERE sr.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_series := p_subject_id;
    ELSIF p_subject_kind = 'library' THEN
        PERFORM 1 FROM libraries l WHERE l.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_library := p_subject_id;
    ELSIF p_subject_kind = 'dup_group' THEN
        SELECT dg.library_id INTO v_library FROM duplicate_groups dg WHERE dg.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
    ELSIF p_subject_kind = 'catalog' THEN
        IF p_subject_id IS DISTINCT FROM v_catalog THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
    ELSE
        RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
        RETURN;
    END IF;

    v_entry := COALESCE(p_reason, '{}'::jsonb) || jsonb_build_object('at', to_jsonb(now()));
    v_from_stage := COALESCE(
        p_reason ->> 'from_stage',
        (SELECT pj0.stage FROM processing_jobs pj0 WHERE pj0.id = p_triggered_by)
    );

    -- a forced trigger (someone asked for it now) runs now: it takes over a pending job, or
    -- one a stage gave back for later (JobDeferred: paused until not_before), and clears
    -- not_before, which a non-forced trigger left (a debounce, a retry's backoff, "guide
    -- reloaded less than N min ago"). A job parked on a dependency (parked_on) and a job
    -- that is running keep their rules: the force becomes the follow-up's, as before.
    IF p_force THEN
        UPDATE processing_jobs pj
           SET priority = LEAST(pj.priority, COALESCE(p_priority, 100)),
               force = true,
               force_cascade = pj.force_cascade OR p_force_cascade,
               reasons = processing_jobs_reason_prepend(pj.reasons, v_entry),
               payload = processing_jobs_payload_merge(pj.payload, p_payload),
               enqueue_count = pj.enqueue_count + 1,
               not_before = NULL
         WHERE pj.subject_kind = p_subject_kind
           AND pj.subject_id = p_subject_id
           AND pj.stage = p_stage
           AND pj.parked_on IS NULL
           AND (pj.state = 'pending'
                OR (pj.state = 'paused' AND pj.not_before > now()))
        RETURNING pj.id INTO v_id;
        IF v_id IS NOT NULL THEN
            RETURN QUERY SELECT v_id, 'coalesced'::TEXT;
            RETURN;
        END IF;
    END IF;

    INSERT INTO processing_jobs AS pj
        (subject_kind, subject_id, video_id, library_id, series_id, season_id,
         stage, state, priority, max_attempts, payload, not_before,
         force, force_cascade, reasons, triggered_by, triggered_by_stage)
    VALUES
        (p_subject_kind, p_subject_id, v_video, v_library, v_series, v_season,
         p_stage, 'pending', COALESCE(p_priority, 100),
         COALESCE(p_max_attempts, s.max_attempts), p_payload,
         CASE WHEN s.debounce_sec > 0
              THEN now() + make_interval(secs => s.debounce_sec) END,
         p_force, p_force_cascade, jsonb_build_array(v_entry), p_triggered_by, v_from_stage)
    ON CONFLICT (subject_kind, subject_id, stage)
       WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused')
    DO UPDATE SET
        priority = CASE WHEN pj.state = 'pending'
                        THEN LEAST(pj.priority, EXCLUDED.priority) ELSE pj.priority END,
        force = pj.force OR (pj.state = 'pending' AND EXCLUDED.force),
        force_cascade = pj.force_cascade OR (pj.state = 'pending' AND EXCLUDED.force_cascade),
        reasons = CASE WHEN pj.state = 'pending'
                       THEN processing_jobs_reason_prepend(pj.reasons, v_entry)
                       ELSE pj.reasons END,
        payload = processing_jobs_payload_merge(pj.payload, EXCLUDED.payload),
        enqueue_count = pj.enqueue_count + 1,
        not_before = CASE WHEN pj.state = 'pending' AND s.debounce_sec > 0
                          THEN LEAST(now() + make_interval(secs => s.debounce_sec),
                                     pj.created_at + make_interval(secs => s.debounce_max_sec))
                          ELSE pj.not_before END,
        rerun_requested = pj.rerun_requested OR pj.state <> 'pending',
        rerun_force = pj.rerun_force
                      OR (pj.state <> 'pending' AND (EXCLUDED.force OR EXCLUDED.force_cascade)),
        rerun_force_cascade = pj.rerun_force_cascade
                              OR (pj.state <> 'pending' AND EXCLUDED.force_cascade),
        rerun_reasons = CASE WHEN pj.state <> 'pending'
                             THEN processing_jobs_reason_prepend(pj.rerun_reasons, v_entry)
                             ELSE pj.rerun_reasons END
    RETURNING pj.id,
              CASE WHEN pj.xmax = 0 THEN 'inserted'
                   WHEN pj.state = 'pending' THEN 'coalesced'
                   ELSE 'rerun_requested' END
         INTO v_id, v_outcome;

    RETURN QUERY SELECT v_id, v_outcome;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- every library's YouTube channel becomes a source, off: nothing downloads until the
-- owner turns one on
INSERT INTO youtube_sources (library_id, kind, url, external_id, mode, added_at, status, error)
SELECT yc.library_id, 'channel', yc.url, yc.channel_id, 'off', now(), yc.status, yc.error
  FROM youtube_channels yc
 WHERE yc.library_id IS NOT NULL
ON CONFLICT (library_id, kind, external_id) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the workers convert, merge, download and purge; the API (an admin) decides
        GRANT SELECT, INSERT, UPDATE ON file_actions TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE, DELETE ON video_copies TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE, DELETE ON duplicate_groups TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE, DELETE ON duplicate_members TO maktaba_worker;
        GRANT SELECT, UPDATE ON youtube_sources TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE, DELETE ON youtube_downloads TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- dev only (README rule 6): the 0144 enqueue, the 0088 subject CHECK
CREATE OR REPLACE FUNCTION processing_jobs_enqueue(
    p_subject_kind   TEXT,
    p_subject_id     UUID,
    p_stage          TEXT,
    p_priority       INT     DEFAULT NULL,
    p_force          BOOLEAN DEFAULT false,
    p_force_cascade  BOOLEAN DEFAULT false,
    p_reason         JSONB   DEFAULT '{}'::jsonb,
    p_triggered_by   BIGINT  DEFAULT NULL,
    p_payload        JSONB   DEFAULT NULL,
    p_max_attempts   INT     DEFAULT NULL
) RETURNS TABLE (job_id BIGINT, outcome TEXT)
LANGUAGE plpgsql AS $$
#variable_conflict use_column
DECLARE
    s            job_stages%ROWTYPE;
    v_library    UUID;
    v_video      UUID;
    v_series     UUID;
    v_season     UUID;
    v_entry      JSONB;
    v_from_stage TEXT;
    v_id         BIGINT;
    v_outcome    TEXT;
    v_catalog    CONSTANT UUID := '00000000-0000-0000-0000-00000000ca7a';
BEGIN
    SELECT * INTO s FROM job_stages WHERE stage = p_stage;
    IF NOT FOUND THEN
        RETURN QUERY SELECT NULL::BIGINT, 'unknown_stage'::TEXT;
        RETURN;
    END IF;
    IF NOT s.enabled THEN
        RETURN QUERY SELECT NULL::BIGINT, 'disabled'::TEXT;
        RETURN;
    END IF;
    IF NOT (p_subject_kind = ANY (s.subject_kinds)) THEN
        RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
        RETURN;
    END IF;

    IF p_subject_kind = 'video' THEN
        SELECT v.library_id INTO v_library FROM videos v WHERE v.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_video := p_subject_id;
    ELSIF p_subject_kind = 'season' THEN
        SELECT ss.library_id INTO v_library FROM series_seasons ss WHERE ss.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_season := p_subject_id;
    ELSIF p_subject_kind = 'series' THEN
        SELECT sr.library_id INTO v_library FROM series sr WHERE sr.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_series := p_subject_id;
    ELSIF p_subject_kind = 'library' THEN
        PERFORM 1 FROM libraries l WHERE l.id = p_subject_id;
        IF NOT FOUND THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
        v_library := p_subject_id;
    ELSIF p_subject_kind = 'catalog' THEN
        IF p_subject_id IS DISTINCT FROM v_catalog THEN
            RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
            RETURN;
        END IF;
    ELSE
        RETURN QUERY SELECT NULL::BIGINT, 'invalid_subject'::TEXT;
        RETURN;
    END IF;

    v_entry := COALESCE(p_reason, '{}'::jsonb) || jsonb_build_object('at', to_jsonb(now()));
    v_from_stage := COALESCE(
        p_reason ->> 'from_stage',
        (SELECT pj0.stage FROM processing_jobs pj0 WHERE pj0.id = p_triggered_by)
    );

    -- a forced trigger (someone asked for it now) runs now: it takes over a pending job, or
    -- one a stage gave back for later (JobDeferred: paused until not_before), and clears
    -- not_before, which a non-forced trigger left (a debounce, a retry's backoff, "guide
    -- reloaded less than N min ago"). A job parked on a dependency (parked_on) and a job
    -- that is running keep their rules: the force becomes the follow-up's, as before.
    IF p_force THEN
        UPDATE processing_jobs pj
           SET priority = LEAST(pj.priority, COALESCE(p_priority, 100)),
               force = true,
               force_cascade = pj.force_cascade OR p_force_cascade,
               reasons = processing_jobs_reason_prepend(pj.reasons, v_entry),
               payload = processing_jobs_payload_merge(pj.payload, p_payload),
               enqueue_count = pj.enqueue_count + 1,
               not_before = NULL
         WHERE pj.subject_kind = p_subject_kind
           AND pj.subject_id = p_subject_id
           AND pj.stage = p_stage
           AND pj.parked_on IS NULL
           AND (pj.state = 'pending'
                OR (pj.state = 'paused' AND pj.not_before > now()))
        RETURNING pj.id INTO v_id;
        IF v_id IS NOT NULL THEN
            RETURN QUERY SELECT v_id, 'coalesced'::TEXT;
            RETURN;
        END IF;
    END IF;

    INSERT INTO processing_jobs AS pj
        (subject_kind, subject_id, video_id, library_id, series_id, season_id,
         stage, state, priority, max_attempts, payload, not_before,
         force, force_cascade, reasons, triggered_by, triggered_by_stage)
    VALUES
        (p_subject_kind, p_subject_id, v_video, v_library, v_series, v_season,
         p_stage, 'pending', COALESCE(p_priority, 100),
         COALESCE(p_max_attempts, s.max_attempts), p_payload,
         CASE WHEN s.debounce_sec > 0
              THEN now() + make_interval(secs => s.debounce_sec) END,
         p_force, p_force_cascade, jsonb_build_array(v_entry), p_triggered_by, v_from_stage)
    ON CONFLICT (subject_kind, subject_id, stage)
       WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused')
    DO UPDATE SET
        priority = CASE WHEN pj.state = 'pending'
                        THEN LEAST(pj.priority, EXCLUDED.priority) ELSE pj.priority END,
        force = pj.force OR (pj.state = 'pending' AND EXCLUDED.force),
        force_cascade = pj.force_cascade OR (pj.state = 'pending' AND EXCLUDED.force_cascade),
        reasons = CASE WHEN pj.state = 'pending'
                       THEN processing_jobs_reason_prepend(pj.reasons, v_entry)
                       ELSE pj.reasons END,
        payload = processing_jobs_payload_merge(pj.payload, EXCLUDED.payload),
        enqueue_count = pj.enqueue_count + 1,
        not_before = CASE WHEN pj.state = 'pending' AND s.debounce_sec > 0
                          THEN LEAST(now() + make_interval(secs => s.debounce_sec),
                                     pj.created_at + make_interval(secs => s.debounce_max_sec))
                          ELSE pj.not_before END,
        rerun_requested = pj.rerun_requested OR pj.state <> 'pending',
        rerun_force = pj.rerun_force
                      OR (pj.state <> 'pending' AND (EXCLUDED.force OR EXCLUDED.force_cascade)),
        rerun_force_cascade = pj.rerun_force_cascade
                              OR (pj.state <> 'pending' AND EXCLUDED.force_cascade),
        rerun_reasons = CASE WHEN pj.state <> 'pending'
                             THEN processing_jobs_reason_prepend(pj.rerun_reasons, v_entry)
                             ELSE pj.rerun_reasons END
    RETURNING pj.id,
              CASE WHEN pj.xmax = 0 THEN 'inserted'
                   WHEN pj.state = 'pending' THEN 'coalesced'
                   ELSE 'rerun_requested' END
         INTO v_id, v_outcome;

    RETURN QUERY SELECT v_id, v_outcome;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM processing_jobs WHERE subject_kind = 'dup_group';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_subject_chk;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs
    ADD CONSTRAINT processing_jobs_subject_chk CHECK (
        subject_id IS NOT NULL AND (
            (subject_kind = 'video'   AND video_id   = subject_id)
         OR (subject_kind = 'library' AND library_id = subject_id AND video_id IS NULL)
         OR (subject_kind = 'series'  AND series_id  = subject_id AND video_id IS NULL)
         OR (subject_kind = 'season'  AND season_id  = subject_id AND video_id IS NULL)
         OR (subject_kind = 'catalog'
             AND subject_id = '00000000-0000-0000-0000-00000000ca7a'::uuid
             AND video_id IS NULL)
        )
    );
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM processing_jobs WHERE stage IN ('convert_mp4', 'dedup_scan', 'dedup_apply', 'trash_purge', 'youtube_fetch', 'youtube_backfill');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('convert_mp4', 'dedup_scan', 'dedup_apply', 'trash_purge', 'youtube_fetch', 'youtube_backfill');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM worker_pools WHERE pool = 'convert';
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_full_hash_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_superseded_by_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS superseded_by;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS full_hash;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_downloads;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_sources;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS duplicate_members;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS duplicate_groups;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS video_copies;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS file_actions;
-- +goose StatementEnd
