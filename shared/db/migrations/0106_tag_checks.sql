-- +goose Up
-- +goose StatementBegin
--
-- Slot 0106 (parity with mediaintel check-tags / check-clips) - the language model's answers,
-- kept per input so a question is never asked twice. tag_checks: one row per season (the
-- model's topics for the series, cached under a hash of its name, library, sampled episode
-- titles, model and prompt version). clip_checks: one row per short video (the topics it
-- keeps, cached under a hash of its library, title, candidate topics, model and prompt
-- version). applied_hash is the hash of the tags the answer was last applied to: tags that
-- already carry the verdict are left alone, so the steps run again without asking.
--
CREATE TABLE IF NOT EXISTS tag_checks (
    season_id     UUID        PRIMARY KEY REFERENCES series_seasons(id) ON DELETE CASCADE,
    input_hash    TEXT        NOT NULL,
    model         TEXT        NOT NULL,
    answer        TEXT        NOT NULL,
    applied_hash  TEXT,
    checked_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS clip_checks (
    video_id      UUID        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    input_hash    TEXT        NOT NULL,
    model         TEXT        NOT NULL,
    topics        TEXT        NOT NULL,
    applied_hash  TEXT,
    checked_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS clip_checks;
DROP TABLE IF EXISTS tag_checks;
-- +goose StatementEnd
