-- +goose Up
-- +goose StatementBegin
--
-- A full machine (a copy of this database) is the home admin's decision, never the
-- machine's own (Epic 42, the API enforces roles): until now an approved machine made
-- itself full by reporting so (cluster_node_report_full, slot 0135), and full_node is
-- what grants a copy (cluster_node_replica_grant, slot 0147).
--
--   full_capable  what the machine reports it can be (it runs maktaba-server as a node)
--   full_allowed  what an admin allowed (the Machines page; the api checks the admin)
--   full_node     both: what every check of a copy reads, unchanged
--
-- Machines already full keep their copy (allowed as they are); an admin can take it back.
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS full_capable boolean NOT NULL DEFAULT false;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS full_allowed boolean NOT NULL DEFAULT false;
UPDATE cluster_nodes SET full_capable = full_node, full_allowed = full_node;
-- +goose StatementEnd

-- +goose StatementBegin
-- The machine's report sets full_capable only; full_node follows the admin's full_allowed.
-- Same signature as slot 0135, so its grants (the node roles, slot 0147) stay.
CREATE OR REPLACE FUNCTION cluster_node_report_full(
    p_node uuid, p_version text, p_platform text, p_web_url text, p_full_node boolean, p_vod jsonb
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM cluster_node_guard(p_node);
    UPDATE cluster_nodes n SET
        version = COALESCE(NULLIF(p_version, ''), n.version),
        platform = COALESCE(NULLIF(p_platform, ''), n.platform),
        web_url = COALESCE(NULLIF(p_web_url, ''), n.web_url),
        full_capable = COALESCE(p_full_node, n.full_capable),
        full_node = COALESCE(p_full_node, n.full_capable) AND n.full_allowed,
        vod = COALESCE(p_vod, n.vod),
        full_reported_at = now()
     WHERE n.id = p_node;
END
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cluster_node_report_full(
    p_node uuid, p_version text, p_platform text, p_web_url text, p_full_node boolean, p_vod jsonb
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM cluster_node_guard(p_node);
    UPDATE cluster_nodes n SET
        version = COALESCE(NULLIF(p_version, ''), n.version),
        platform = COALESCE(NULLIF(p_platform, ''), n.platform),
        web_url = COALESCE(NULLIF(p_web_url, ''), n.web_url),
        full_node = COALESCE(p_full_node, n.full_node),
        vod = COALESCE(p_vod, n.vod),
        full_reported_at = now()
     WHERE n.id = p_node;
END
$f$;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS full_allowed;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS full_capable;
-- +goose StatementEnd
