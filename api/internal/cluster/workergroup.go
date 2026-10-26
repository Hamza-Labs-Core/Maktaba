package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// The machines' group role (WorkerGroup) and what it may do on the home's database:
// slot 0103 made it, and every later migration that added a table of secrets revoked that
// table from it (default privileges give the group data on new tables). A home whose
// database lost the group (a restore into a cluster that never had it: roles are the
// cluster's, not a dump's) cannot give its machines roles (ReasonNoGroup): the home makes
// it again here, with every grant and revoke the migrations gave it, once its own user may
// create roles (EnsureWorkerGroup). A migration that grants or revokes on maktaba_worker
// adds its line here too: TestWorkerGroupAsTheMigrationsMadeIt fails until it does.

// workerGroupGrants are the group's privileges as the migrations left them (slot 0147's
// block for a home without the group, with the slots after it), for the group named
// maktaba_worker (groupSQL names another for the tests).
const workerGroupGrants = `
-- slot 0103: data, no schema
EXECUTE format('GRANT CONNECT ON DATABASE %I TO maktaba_worker', current_database());
GRANT USAGE ON SCHEMA public TO maktaba_worker;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO maktaba_worker;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO maktaba_worker;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO maktaba_worker;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO maktaba_worker;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO maktaba_worker;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT EXECUTE ON FUNCTIONS TO maktaba_worker;
REVOKE ALL ON users, web_sessions, refresh_tokens, personal_access_tokens,
    password_reset_tokens, pairing_tickets, licenses, devices, device_downloads,
    streaming_sessions, watch_sessions, user_recs, user_analytics_prefs,
    idempotency_keys, instance_identity, cluster_nodes, cluster_node_stages
    FROM maktaba_worker;
REVOKE ALL ON audit_log FROM maktaba_worker;
GRANT INSERT ON audit_log TO maktaba_worker;
-- slot 0119
REVOKE ALL ON cluster_metrics_cache, cluster_log_cache, cluster_cache_files FROM maktaba_worker;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON health_samples FROM maktaba_worker;
-- slot 0120
REVOKE ALL ON home_secrets FROM maktaba_worker;
-- slot 0123
REVOKE ALL ON streaming_hosts, channel_placements, tv_cluster_secret FROM maktaba_worker;
REVOKE ALL ON SEQUENCE tv_placement_generation_seq FROM maktaba_worker;
GRANT SELECT ON streaming_hosts, channel_placements TO maktaba_worker;
REVOKE EXECUTE ON FUNCTION tv_host_guard(text) FROM maktaba_worker;
GRANT EXECUTE ON FUNCTION tv_host_beat(text, jsonb), tv_placement_ack(uuid, bigint),
    tv_placement_release(uuid, bigint) TO maktaba_worker;
-- slot 0124
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON storage_servers FROM maktaba_worker;
GRANT SELECT ON storage_servers TO maktaba_worker;
REVOKE ALL ON storage_path_migrations FROM maktaba_worker;
-- slot 0125
REVOKE ALL ON tv_remote_inputs, tv_remote_pushes, tv_remote_sessions,
    tv_remote_usage_daily, tv_push_secrets FROM maktaba_worker;
GRANT SELECT ON tv_remote_inputs, tv_remote_pushes TO maktaba_worker;
REVOKE EXECUTE ON FUNCTION tv_push_guard() FROM maktaba_worker;
GRANT EXECUTE ON FUNCTION tv_push_credentials(uuid), tv_push_report(uuid, text, text)
    TO maktaba_worker;
IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    REVOKE INSERT, UPDATE, DELETE ON goose_db_version FROM maktaba_worker;
END IF;
-- slot 0126
REVOKE ALL ON tv_simulcast_destinations, tv_simulcast_attachments, tv_simulcast_outputs
    FROM maktaba_worker;
-- slot 0129
REVOKE ALL ON tv_slots FROM maktaba_worker;
GRANT SELECT ON tv_slots TO maktaba_worker;
GRANT EXECUTE ON FUNCTION tv_slot_beat(text, text, jsonb) TO maktaba_worker;
-- slot 0130
REVOKE ALL ON notifications, notification_reads, notification_cursors FROM maktaba_worker;
-- slot 0131
REVOKE ALL ON storage_canonicalize FROM maktaba_worker;
REVOKE ALL ON storage_path_backups FROM maktaba_worker;
-- slot 0132
REVOKE ALL ON release_catalog, update_runs, update_run_steps, update_targets,
    update_watermark FROM maktaba_worker;
-- slot 0134
REVOKE ALL ON cluster_sso_codes FROM maktaba_worker;
-- slot 0135
GRANT EXECUTE ON FUNCTION cluster_node_report_full(uuid, text, text, text, boolean, jsonb)
    TO maktaba_worker;
GRANT SELECT, INSERT, UPDATE ON streaming_sessions TO maktaba_worker;
-- slot 0136
REVOKE ALL ON tv_swap_ops, tv_channel_switch FROM maktaba_worker;
GRANT SELECT ON tv_swap_ops, tv_channel_switch TO maktaba_worker;
-- slot 0137
REVOKE ALL ON tv_slot_events FROM maktaba_worker;
GRANT SELECT ON tv_slot_events TO maktaba_worker;
-- slot 0139
REVOKE ALL ON enrich_usage FROM maktaba_worker;
GRANT SELECT ON enrich_usage TO maktaba_worker;
-- slot 0140
REVOKE ALL ON graphql_persisted_queries FROM maktaba_worker;
-- slot 0143
REVOKE ALL ON failure_ignores FROM maktaba_worker;
GRANT SELECT ON failure_ignores TO maktaba_worker;
REVOKE ALL ON item_requests FROM maktaba_worker;
GRANT SELECT, UPDATE ON item_requests TO maktaba_worker;
GRANT SELECT, INSERT, UPDATE ON dependency_health TO maktaba_worker;
GRANT SELECT, INSERT, UPDATE ON item_checks TO maktaba_worker;
-- slot 0147 (after the group's block)
REVOKE ALL ON cluster_replica_beat FROM maktaba_worker;
-- slot 0148
REVOKE ALL ON search_runs FROM maktaba_worker;
-- slot 0150
REVOKE ALL ON cluster_certs FROM maktaba_worker;
-- slot 0152
REVOKE ALL ON semantic_store_state FROM maktaba_worker;
GRANT SELECT ON semantic_store_state TO maktaba_worker;
`

// groupNameRe is a group role's name groupSQL accepts.
var groupNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// groupSQL makes group (absent) with the worker group's privileges: one DO block, run as
// a user that may create roles and owns the tables (the home's own).
func groupSQL(group string) (string, error) {
	if !groupNameRe.MatchString(group) {
		return "", fmt.Errorf("cluster: invalid group role name %q", group)
	}
	grants := strings.ReplaceAll(workerGroupGrants, "maktaba_worker", group)
	return "DO $$\nBEGIN\n    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '" + group + "') THEN\n" +
		"        CREATE ROLE " + group + " NOLOGIN;\n" + indent(grants) + "    END IF;\nEND\n$$;", nil
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.Trim(s, "\n"), "\n") {
		if l == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("        " + l + "\n")
	}
	return b.String()
}

// EnsureWorkerGroup makes the machines' group role again when the home's database lacks
// it, with every privilege the migrations gave it (db: the home's own user, which may
// create roles: a bundled Postgres's after its grants were mended). A group that exists is
// left as it is.
func EnsureWorkerGroup(ctx context.Context, db *sql.DB) error {
	return ensureGroup(ctx, db, WorkerGroup)
}

func ensureGroup(ctx context.Context, db *sql.DB, group string) error {
	q, err := groupSQL(group)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("cluster: make the %s group role again: %w", group, err)
	}
	return nil
}
