-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0037 (Epic 7 / Story 7.9) — saved search definitions per user.
--
-- ``query`` is the JSON body of a previous POST /api/search request,
-- replayable verbatim. ``is_smart`` flips this row into a
-- "smart collection" source (Epic 9 Story 9.14) — same schema, different
-- consumer.
--
CREATE TABLE IF NOT EXISTS saved_searches (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT         NOT NULL,
    query       JSONB        NOT NULL,
    is_smart    BOOLEAN      NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS saved_searches;
-- +goose StatementEnd
