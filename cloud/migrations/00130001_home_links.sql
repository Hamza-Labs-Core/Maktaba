-- +goose Up
-- Linked accounts (Epic 42 story 42.7). Each home tells the cloud which
-- cloud accounts it has linked to its users, by cloud user id only: the
-- cloud lists those homes to the account and signs a sign-in assertion
-- for them. The home user, the link's scope and every right stay in the
-- home's own database (home_user_links there); a row here grants
-- nothing by itself, the home decides each sign-in.
CREATE TABLE IF NOT EXISTS home_links (
    server_id     UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    cloud_user_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reported_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, cloud_user_id)
);
CREATE INDEX IF NOT EXISTS idx_home_links_user ON home_links (cloud_user_id);

-- +goose Down
DROP TABLE IF EXISTS home_links;
