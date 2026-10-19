-- +goose Up
-- +goose StatementBegin
--
-- Slot 0134 (Epic 35 stories 35.8 and 35.12, README slot S3): what a full node reports
-- about itself (specs/epics/35-cluster-full-nodes-and-updates/design.md §9.1, §11.5).
--
--   version, platform   the node's release and OS/arch (os-arch, e.g. windows-amd64)
--   update_state        the rollout's view of the machine (story 35.12): state, run,
--                       prepared version, detail
--   full_node           true once the machine runs maktaba-server in node mode (story
--                       35.8); false: a worker-only node (the pipeline alone)
--   web_url             the machine's own web address (the Machines page links to it)
--   vod                 the VOD fallback (design §11.5): {fallback, served_by, since,
--                       detail} when this machine's storage check cannot read the share
--   full_reported_at    when the node last reported these
--
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS version text;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS platform text;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS update_state jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS full_node boolean NOT NULL DEFAULT false;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS web_url text;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS vod jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS full_reported_at timestamptz;
-- +goose StatementEnd

-- +goose StatementBegin
-- A full node's report of itself, for its own database role only (cluster_node_guard,
-- slot 0103): the node role has no access to cluster_nodes.
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
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION cluster_node_report_full(uuid, text, text, text, boolean, jsonb) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT EXECUTE ON FUNCTION cluster_node_report_full(uuid, text, text, text, boolean, jsonb)
            TO maktaba_worker;
        -- a full node serves video on demand itself (design §9.1, R13): its API records
        -- the sessions it opens on its own streaming role (slot 0103 revoked the table
        -- as viewing data; reading and writing session rows is what serving VOD is)
        GRANT SELECT, INSERT, UPDATE ON streaming_sessions TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        REVOKE ALL ON streaming_sessions FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS cluster_node_report_full(uuid, text, text, text, boolean, jsonb);
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS full_reported_at;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS vod;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS web_url;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS full_node;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS update_state;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS platform;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS version;
-- +goose StatementEnd
