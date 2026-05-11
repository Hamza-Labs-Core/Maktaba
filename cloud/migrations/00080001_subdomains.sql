-- +goose Up
-- Subdomain provisioning state and reserved-slug guard list.
CREATE TABLE IF NOT EXISTS subdomains (
    slug         TEXT        PRIMARY KEY,
    server_id    UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    provisioned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    cert_renewed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS reserved_slugs (
    slug TEXT PRIMARY KEY,
    reason TEXT NOT NULL
);

INSERT INTO reserved_slugs (slug, reason) VALUES
    ('admin','operator'),
    ('api','operator'),
    ('app','operator'),
    ('docs','operator'),
    ('help','operator'),
    ('maktaba','operator'),
    ('relay','operator'),
    ('status','operator'),
    ('web','operator'),
    ('www','operator')
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS reserved_slugs;
DROP TABLE IF EXISTS subdomains;
