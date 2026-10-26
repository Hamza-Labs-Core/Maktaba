-- +goose Up
-- +goose StatementBegin
--
-- Slot 0153 (Epic 34, story 34.1) - the public TV site: which channels the owner made
-- public, with the admin's rights confirmation (who, when, a note). A row exists for
-- every channel that was ever public: making a channel private clears only `public`.
-- The confirmation stays, for audit, and must be given again next time (design §2.1).
--
-- `slug` is the channel's address on the site (/watch/<slug>), unique, [a-z0-9-]. The
-- site never sees the channel id. A public channel's push is held always-on by the home
-- (story 34.3, through Epic 32's push and relay). The site keeps its own state in the
-- Worker, not here.
--
-- maktaba_worker keeps the default rights (slot 0103) on this table, as on every
-- ordinary table: narrowing them here would be undone by an older release remaking
-- its worker group (compat-prev), as slot 0152 found. The rows hold no secret. So that a
-- machine cannot make a channel public by writing a row, `rights_sig` is an HMAC by the
-- home (its key in home_secrets, which machines cannot read) over the channel, the flag,
-- the slug and the confirmation: the home ignores a public row whose signature does not
-- match.
--
CREATE TABLE IF NOT EXISTS tv_public_channels (
    channel_id           UUID        PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    public               BOOLEAN     NOT NULL DEFAULT false,
    slug                 TEXT        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,39}$'),
    rights_confirmed_by  TEXT,
    rights_confirmed_at  TIMESTAMPTZ,
    rights_note          TEXT        NOT NULL DEFAULT '',
    rights_sig           TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by           TEXT
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tv_public_channels;
-- +goose StatementEnd
