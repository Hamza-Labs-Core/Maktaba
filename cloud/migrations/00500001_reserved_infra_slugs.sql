-- +goose Up
-- Story 50.01: names the cloud's own infrastructure uses under *.relay.maktaba.tv
-- (docs/cloud-dns.md), so no home can take them as its slug: the canary home, the
-- Phase 2 relays r1…r99, the direct-remote and staging names, and the plugin store.
INSERT INTO reserved_slugs (slug, reason) VALUES
    ('canary','infrastructure'),
    ('direct','infrastructure'),
    ('staging','infrastructure'),
    ('plugins','infrastructure'),
    ('plugins-cdn','infrastructure')
ON CONFLICT DO NOTHING;

INSERT INTO reserved_slugs (slug, reason)
SELECT 'r' || n, 'infrastructure' FROM generate_series(1, 99) AS n
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM reserved_slugs WHERE reason = 'infrastructure';
