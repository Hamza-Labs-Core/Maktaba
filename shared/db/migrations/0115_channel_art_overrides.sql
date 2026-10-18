-- +goose Up
-- +goose StatementBegin
--
-- Slot 0115 (Epic 27, channel art) - channel art set by hand: an uploaded logo, corner
-- watermark or loading screen, or the watermark's corner and size. channels.logo_path and
-- channels.mode_config->'look' keep holding the effective art (what the stream engine reads);
-- a row here says that piece was set by a person, so the lineup plan writer keeps it instead
-- of treating the channel as user-edited or overwriting it. Deleting the row goes back to
-- the plan's value.
--
--   path      the hand-set image (a file under <data_dir>/channel-art/<channel id>/, an
--             absolute image path or an http(s) URL); NULL = the plan's image (watermark
--             settings only)
--   settings  watermark only: {"position", "width_pct", "margin_pct"} set by hand
--   base      the value this override replaced (a path string or the watermark object),
--             what a revert restores on a channel no plan owns
--
CREATE TABLE IF NOT EXISTS channel_art_overrides (
    channel_id  UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    kind        TEXT        NOT NULL CHECK (kind IN ('logo','watermark','loading')),
    path        TEXT,
    settings    JSONB,
    base        JSONB,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (channel_id, kind)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_art_overrides;
-- +goose StatementEnd
