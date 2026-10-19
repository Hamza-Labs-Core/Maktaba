-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0120 (Epic 9 / Stories 9.9 and 9.18) - video_embeddings: one vector per video, the
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
-- chapter_infer, topic_assign and topic_recluster move from the legacy media lane onto the
-- engine (Transcription lane, after index). Workers re-sync job_stages from the graph at
-- start; this keeps a fresh database consistent first.
UPDATE job_stages SET legacy = false, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Chapters', lane_order = 5
    WHERE stage = 'chapter_infer';
UPDATE job_stages SET legacy = false, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Topics', lane_order = 6
    WHERE stage = 'topic_assign';
UPDATE job_stages SET legacy = false, lane = 'speech', pool = 'cpu', version = 1,
    label = 'Library topics', lane_order = 7, debounce_sec = 1800, debounce_max_sec = 21600
    WHERE stage = 'topic_recluster';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET legacy = true, lane = 'media', pool = 'cpu', version = 1,
    debounce_sec = 0, debounce_max_sec = 0,
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
