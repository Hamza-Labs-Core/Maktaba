-- +goose Up
-- +goose NO TRANSACTION
-- +goose StatementBegin
--
-- Slot 0096 (plan 06-11, review of #47) - a "force cascade" trigger that arrives
-- while the job runs is kept for its follow-up: until now it was folded into
-- rerun_force only, so the follow-up ran forced but, if its output hash was the
-- same, never propagated - the cascade the admin asked for silently did not happen.
--
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS rerun_force_cascade BOOLEAN NOT NULL DEFAULT false;
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
-- +goose NO TRANSACTION
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
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS rerun_force_cascade;
-- +goose StatementEnd
