-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0177 (Epic 51, story 51.06) - a component's stages stop without losing work.
--
--   processing_jobs_enqueue   a stage switched off by its component
--                             (job_stages.disabled_reason = 'component_off') answers
--                             'skipped_component_off' and makes no job (the edge is logged
--                             as skipped; the component_catchup job makes them when the
--                             component comes back). A stage whose component is held
--                             ('component_held': stopped until an admin starts it) still
--                             queues: the job waits, since claiming needs enabled. Every
--                             other rule is 0166's.
--   processing_jobs_payload_merge
--                             also unions "components" (the component_catchup payload:
--                             one entry per component to catch up), as it does "seasons",
--                             so two components enabled at once share one catch-up job.
--
-- SQLite has neither function: engine/enqueue.py carries the same two rules.
-- Additive: two functions replaced, no table changed.
--
CREATE OR REPLACE FUNCTION processing_jobs_payload_merge(p_old JSONB, p_new JSONB)
RETURNS JSONB LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    merged JSONB;
    v_out  JSONB;
BEGIN
    IF p_old IS NULL THEN
        RETURN p_new;
    END IF;
    IF p_new IS NULL OR jsonb_typeof(p_new) <> 'object' OR jsonb_typeof(p_old) <> 'object' THEN
        RETURN p_old;
    END IF;
    v_out := p_old;
    IF p_new ? 'seasons' THEN
        SELECT COALESCE(jsonb_agg(DISTINCT s), '[]'::jsonb) INTO merged
          FROM jsonb_array_elements(COALESCE(p_old -> 'seasons', '[]'::jsonb) || (p_new -> 'seasons')) AS s;
        v_out := v_out || jsonb_build_object('seasons', merged);
    END IF;
    IF p_new ? 'components' AND jsonb_typeof(p_new -> 'components') = 'array' THEN
        SELECT COALESCE(jsonb_agg(DISTINCT c), '[]'::jsonb) INTO merged
          FROM jsonb_array_elements(COALESCE(p_old -> 'components', '[]'::jsonb) || (p_new -> 'components')) AS c;
        v_out := v_out || jsonb_build_object('components', merged);
    END IF;
    RETURN v_out;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
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
    -- Epic 51: a component that is off is not fed (its jobs are not made; the edge is
    -- logged as skipped and the catch-up makes them when it comes back); one that is
    -- held (stopped until an admin starts it) still queues its work, which waits
    IF NOT s.enabled AND s.disabled_reason IS DISTINCT FROM 'component_held' THEN
        IF s.disabled_reason = 'component_off' THEN
            RETURN QUERY SELECT NULL::BIGINT, 'skipped_component_off'::TEXT;
        ELSE
            RETURN QUERY SELECT NULL::BIGINT, 'disabled'::TEXT;
        END IF;
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION processing_jobs_payload_merge(p_old JSONB, p_new JSONB)
RETURNS JSONB LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    merged JSONB;
BEGIN
    IF p_old IS NULL THEN
        RETURN p_new;
    END IF;
    IF p_new IS NULL OR jsonb_typeof(p_new) <> 'object' OR jsonb_typeof(p_old) <> 'object'
       OR NOT (p_new ? 'seasons') THEN
        RETURN p_old;
    END IF;
    SELECT COALESCE(jsonb_agg(DISTINCT s), '[]'::jsonb) INTO merged
      FROM jsonb_array_elements(COALESCE(p_old -> 'seasons', '[]'::jsonb) || (p_new -> 'seasons')) AS s;
    RETURN p_old || jsonb_build_object('seasons', merged);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
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
