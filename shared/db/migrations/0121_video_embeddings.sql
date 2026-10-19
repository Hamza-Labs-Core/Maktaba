-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0121 (Epic 9 / Stories 9.9 and 9.18) - video_embeddings: one vector per video, the
-- L2-normalised mean of its transcript segments' vectors in the semantic index (Chroma).
-- The engine `chapter_infer` stage writes it while it reads the segment vectors for
-- chapters; `topic_assign` and `topic_recluster` cluster these, so a library's topics never
-- read millions of segment vectors back out of Chroma.
--
-- `vec` is a packed little-endian float32 array of `dim` values (numpy's tobytes(), as
-- library_topics.centroid_vec); `model` is the embedding model, so vectors of two models
-- are never compared.
--
CREATE TABLE IF NOT EXISTS video_embeddings (
    video_id       UUID         PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    library_id     UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    transcript_id  UUID         NOT NULL,
    model          TEXT         NOT NULL,
    dim            INTEGER      NOT NULL CHECK (dim > 0),
    segments       INTEGER      NOT NULL CHECK (segments > 0),
    vec            BYTEA        NOT NULL,
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_embeddings_library_idx
    ON video_embeddings (library_id, model);
-- +goose StatementEnd

-- +goose StatementBegin
-- the embedding model a library's topics were clustered from: topic_assign never scores a
-- vector against centroids of another model, even one of the same dimension
ALTER TABLE library_topics ADD COLUMN IF NOT EXISTS model TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
-- chapter_infer, topic_assign and topic_recluster move from the legacy media lane onto the
-- engine (Transcription lane, after index), and chapter_thumbs joins them. Workers re-sync
-- job_stages from the graph at start; this keeps a fresh database consistent first.
UPDATE job_stages SET legacy = false, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Chapters', lane_order = 5, placement = 'home'
    WHERE stage = 'chapter_infer';
UPDATE job_stages SET legacy = false, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Topics', lane_order = 7
    WHERE stage = 'topic_assign';
UPDATE job_stages SET legacy = false, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Library topics', lane_order = 8, debounce_sec = 1800, debounce_max_sec = 21600
    WHERE stage = 'topic_recluster';
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('chapter_thumbs', 'Chapter frames', ARRAY['video']::text[], 'nas_read', 'speech', 6, 1,
     0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill: videos indexed before chapter_infer was an engine stage never had it queued
-- (index's edge to it is new). Queue it once, at bulk priority (as the version sweep does),
-- for every video whose index finished and that has an active transcript and no
-- chapter_infer job of any state yet: bounded by what is indexed, a no-op when re-run.
-- Chapter frames and topics follow through the stage's edges.
INSERT INTO processing_jobs
    (subject_kind, subject_id, video_id, library_id, stage, state, priority, max_attempts,
     reasons, triggered_by_stage)
SELECT 'video', v.id, v.id, v.library_id, 'chapter_infer', 'pending', 500, 3,
       jsonb_build_array(jsonb_build_object(
           'why', 'chapter_infer joined the engine (slot 0121): backfill of indexed videos',
           'by', 'migration:0121', 'at', to_jsonb(now()))),
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

-- +goose StatementBegin
-- Backfill: posters and sprites for the library as it stood (thumbnail now follows
-- content_hash, which already ran for these videos, so its edge never fires for them).
-- Once per live video with a content hash, no poster yet and no thumbnail job of any
-- state; priority 900, behind live work (100) and the chapter backfill (500), so it only
-- runs when the share has nothing else to read. A no-op when re-run.
INSERT INTO processing_jobs
    (subject_kind, subject_id, video_id, library_id, stage, state, priority, max_attempts,
     reasons, triggered_by_stage)
SELECT 'video', v.id, v.id, v.library_id, 'thumbnail', 'pending', 900, 3,
       jsonb_build_array(jsonb_build_object(
           'why', 'posters follow content_hash (slot 0121): backfill of the library',
           'by', 'migration:0121', 'at', to_jsonb(now()))),
       'content_hash'
FROM videos v
WHERE v.deleted_at IS NULL
  AND v.content_hash IS NOT NULL
  AND v.poster_path IS NULL
  AND NOT EXISTS (SELECT 1 FROM processing_jobs t
                  WHERE t.subject_kind = 'video' AND t.subject_id = v.id
                    AND t.stage = 'thumbnail');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM processing_jobs WHERE stage = 'chapter_thumbs';
DELETE FROM job_stages WHERE stage = 'chapter_thumbs';
UPDATE job_stages SET legacy = true, lane = 'media', pool = 'cpu', version = 1,
    debounce_sec = 0, debounce_max_sec = 0, placement = 'any',
    label = CASE stage WHEN 'chapter_infer' THEN 'Infer chapters'
                       WHEN 'topic_assign' THEN 'Assign topics'
                       ELSE 'Recluster topics' END,
    lane_order = CASE stage WHEN 'chapter_infer' THEN 9 WHEN 'topic_assign' THEN 7 ELSE 6 END
    WHERE stage IN ('chapter_infer', 'topic_assign', 'topic_recluster');
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE library_topics DROP COLUMN IF EXISTS model;
-- +goose StatementEnd
-- +goose StatementBegin
DROP INDEX IF EXISTS video_embeddings_library_idx;
DROP TABLE IF EXISTS video_embeddings;
-- +goose StatementEnd
