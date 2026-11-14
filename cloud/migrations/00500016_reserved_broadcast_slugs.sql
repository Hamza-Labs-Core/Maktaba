-- +goose Up
-- Story 50.21: the broadcast edge's names (ingest.maktaba.tv, live.maktaba.tv, watch.maktaba.tv,
-- docs/cloud-dns.md) are reserved as slugs too, as story 50.01 reserved the cloud's other names,
-- so no home's <slug>.relay.maktaba.tv reads like one of them.
INSERT INTO reserved_slugs (slug, reason) VALUES
    ('ingest','infrastructure'),
    ('live','infrastructure'),
    ('watch','infrastructure')
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM reserved_slugs WHERE reason = 'infrastructure' AND slug IN ('ingest', 'live', 'watch');
