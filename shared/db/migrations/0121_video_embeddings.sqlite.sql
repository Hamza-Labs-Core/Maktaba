-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0121 (Epic 9 / Stories 9.9 and 9.18): UUID and
-- TIMESTAMPTZ become TEXT, BYTEA becomes BLOB, arrays and JSONB become JSON text.
--
CREATE TABLE IF NOT EXISTS video_embeddings (
    video_id       TEXT     PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    library_id     TEXT     NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    transcript_id  TEXT     NOT NULL,
    model          TEXT     NOT NULL,
    dim            INTEGER  NOT NULL CHECK (dim > 0),
    segments       INTEGER  NOT NULL CHECK (segments > 0),
    vec            BLOB     NOT NULL,
    updated_at     TEXT     NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS video_embeddings_library_idx
    ON video_embeddings (library_id, model);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE library_topics ADD COLUMN IF NOT EXISTS model TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET legacy = 0, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Chapters', lane_order = 5, placement = 'home'
    WHERE stage = 'chapter_infer';
UPDATE job_stages SET legacy = 0, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Topics', lane_order = 7
    WHERE stage = 'topic_assign';
UPDATE job_stages SET legacy = 0, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Library topics', lane_order = 8, debounce_sec = 1800, debounce_max_sec = 21600
    WHERE stage = 'topic_recluster';
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('chapter_thumbs', 'Chapter frames', '["video"]', 'nas_read', 'speech', 6, 1,
     0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill (see the Postgres file): chapter_infer once for every indexed video with an
-- active transcript and no chapter_infer job yet.
INSERT INTO processing_jobs
    (subject_kind, subject_id, video_id, library_id, stage, state, priority, max_attempts,
     reasons, triggered_by_stage)
SELECT 'video', v.id, v.id, v.library_id, 'chapter_infer', 'pending', 500, 3,
       json_array(json_object(
           'why', 'chapter_infer joined the engine (slot 0121): backfill of indexed videos',
           'by', 'migration:0121', 'at', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))),
       'index'
FROM videos v
WHERE v.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM processing_jobs j
              WHERE j.subject_kind = 'video' AND j.subject_id = v.id
                AND j.stage = 'index' AND j.state = 'done')
  AND EXISTS (SELECT 1 FROM transcripts t WHERE t.video_id = v.id AND t.is_active)
  AND NOT EXISTS (SELECT 1 FROM processing_jobs c
                  WHERE c.subject_kind = 'video' AND c.subject_id = v.id
                    AND c.stage = 'chapter_infer');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM processing_jobs WHERE stage = 'chapter_thumbs';
DELETE FROM job_stages WHERE stage = 'chapter_thumbs';
UPDATE job_stages SET legacy = 1, lane = 'media', pool = 'cpu', version = 1,
    debounce_sec = 0, debounce_max_sec = 0, placement = 'any',
    label = CASE stage WHEN 'chapter_infer' THEN 'Infer chapters'
                       WHEN 'topic_assign' THEN 'Assign topics'
                       ELSE 'Recluster topics' END,
    lane_order = CASE stage WHEN 'chapter_infer' THEN 9 WHEN 'topic_assign' THEN 7 ELSE 6 END
    WHERE stage IN ('chapter_infer', 'topic_assign', 'topic_recluster');
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS video_embeddings_library_idx;
DROP TABLE IF EXISTS video_embeddings;
-- +goose StatementEnd
