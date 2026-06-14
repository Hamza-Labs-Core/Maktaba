-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0078 (Epic 26 / Story 26.6) — enrichment decisions + field
-- provenance.
--
-- `media_field_provenance` is the source of truth for "who owns this
-- field". One row per (video, field) records whether the current value
-- came from the `user`, an `enrichment` accept, or the `parser`, plus
-- the `prev_value` needed to revert. The "never overwrite a user edit"
-- guarantee (Story 26.5/26.6) is enforced here: an accept skips any
-- field whose provenance origin is `user`; a user PATCH upserts
-- origin=`user`, flipping that field to protected.
--
-- `enrichment_decisions` is the accept/dismiss/revert audit trail.
--
CREATE TABLE IF NOT EXISTS media_field_provenance (
    video_id   UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    field      TEXT NOT NULL,
    origin     TEXT NOT NULL CHECK (origin IN ('user','enrichment','parser')),
    prev_value TEXT,
    source_id  TEXT,
    set_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (video_id, field)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS enrichment_decisions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id      UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    external_id   TEXT,
    action        TEXT NOT NULL CHECK (action IN ('accept','dismiss','revert','auto_accept')),
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    applied       JSONB NOT NULL DEFAULT '[]'::jsonb,
    skipped       JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Candidate dismissal lives on the candidate row (mirrors the
-- recommendation-dismissal pattern); re-enrich/manual-search clears it.
ALTER TABLE media_metadata_enrichment
    ADD COLUMN IF NOT EXISTS is_dismissed BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS enrichment_decisions_video_idx
    ON enrichment_decisions (video_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS enrichment_decisions_video_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE media_metadata_enrichment DROP COLUMN IF EXISTS is_dismissed;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS enrichment_decisions;
DROP TABLE IF EXISTS media_field_provenance;
-- +goose StatementEnd
