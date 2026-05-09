-- +goose Up
-- +goose StatementBegin
--
-- Slot 0006 (Story 1.5 / plan-01-05) — per-library scan watermarks
-- and the purge audit log.
--
-- `library_scan_state` is a cache/observability table — the truth
-- lives in `videos`. We carry it because (a) the API needs
-- "last scan finished N minutes ago" without a heavy aggregate over
-- `videos`, and (b) we need a sweep-id watermark so concurrent sweeps
-- don't race on the "transition stragglers to MISSING" step
-- (plan-01-05 §2.3).
--
-- `purge_log` is the audit trail for `--purge-missing` deletions.
-- `video_id` is intentionally NOT a foreign key — by the time the row
-- is written, the videos row has already been hard-deleted.
--
-- Slot 0008 (plan-01-04) extends `library_scan_state` with
-- `cancel_requested` + `progress_pct`. Those columns do not belong on
-- this slot.
--
CREATE TABLE IF NOT EXISTS library_scan_state (
    library_id        UUID         PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    last_scan_at      TIMESTAMPTZ,
    last_scan_id      UUID,
    in_progress       BOOLEAN      NOT NULL DEFAULT false,
    files_seen        INTEGER      NOT NULL DEFAULT 0,
    files_inserted    INTEGER      NOT NULL DEFAULT 0,
    files_updated     INTEGER      NOT NULL DEFAULT 0,
    files_missing     INTEGER      NOT NULL DEFAULT 0,
    metadata          JSONB        NOT NULL DEFAULT '{}'::jsonb,
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS purge_log (
    id             BIGSERIAL    PRIMARY KEY,
    library_id     UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    video_id       UUID         NOT NULL,
    content_hash   TEXT         NOT NULL,
    path           TEXT         NOT NULL,
    missing_since  TIMESTAMPTZ  NOT NULL,
    purged_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS purge_log;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS library_scan_state;
-- +goose StatementEnd
