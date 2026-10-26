package cluster

import (
	"database/sql"
	"os"
	"sort"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// copyReviewed are the tables a machine's copy holds, each looked at for what it holds
// (specs/epics/37-node-replica): no secret, no other machine's state. A new table is
// copied unless cluster_copy_excluded() (slot 0147) leaves it out, so a migration that
// adds one must decide: add it here, or to the exclusion (a secret, a machine's own).
var copyReviewed = []string{
	"app_settings", "audio_cache", "audio_tracks", "books", "channel_art_overrides",
	"channel_audit_channels", "channel_audits", "channel_placements", "channel_plans",
	"channel_programs", "channel_runtime", "channel_schedule_state", "channels", "chapters",
	"clip_checks", "clip_group_items", "clip_groups", "cluster_replica_beat", "cluster_switchovers",
	"cluster_terms", "collection_items",
	"collection_suggestions", "collections", "dependency_health", "display_title_llm",
	"enrich_jobs", "enrich_usage", "enrichment_decisions", "episode_tags", "events", "extensions",
	"external_links", "failure_ignores", "filler_items", "filler_pools",
	"graphql_persisted_queries", "hdhr_device", "hdhr_tuner_leases", "health_samples",
	"instance_identity", "integrity_checks", "item_checks", "item_requests", "job_events",
	"job_stages", "libraries", "library_acl", "library_group_pending", "library_roots",
	"library_scan_state", "library_stats_cache", "library_sweeps", "library_topics",
	"media_entities", "media_features", "media_field_provenance", "media_info",
	"media_metadata_enrichment", "media_parsed_titles", "notification_cursors",
	"notification_reads", "notifications", "personal_access_tokens", "playback_state",
	"plex_items", "plex_livetv_state", "processing_jobs", "promo_links", "purge_log",
	"queue_control", "recommendation_dismissals", "release_catalog", "relisten_attempts", "relisten_seen", "search_runs",
	"saved_searches",
	"search_history", "season_style", "season_tags", "segment_speakers", "series", "series_books",
	"series_episodes", "series_seasons", "speakers", "storage_canonicalize",
	"storage_path_backups", "storage_path_migrations", "storage_servers", "streaming_hosts",
	"streaming_sessions", "stt_usage", "subtitle_files", "tag_checks", "tags", "topic_collections",
	"topic_items", "transcribe_checkpoints", "transcript_segments", "transcript_units",
	"transcript_words", "transcripts", "tv_change_events", "tv_changes", "tv_channel_switch",
	"tv_release_channels", "tv_releases", "tv_remote_inputs", "tv_remote_pushes",
	"tv_remote_sessions", "tv_remote_usage_daily", "tv_routing", "tv_simulcast_attachments",
	"tv_simulcast_outputs", "tv_slot_events", "tv_slots", "tv_swap_ops", "update_run_steps",
	"update_runs", "update_targets", "update_watermark", "user_analytics_prefs", "user_recs",
	"users", "video_classification", "video_embeddings", "video_entities", "video_repairs",
	"video_signals", "video_tags", "video_topic_tags", "video_topics", "videos", "watch_sessions",
	"web_metadata_cache", "web_session_keys", "worker_pools", "workers", "youtube_channels",
	"youtube_imports", "youtube_playlist_items", "youtube_playlists", "youtube_search_cache",
	"youtube_videos",
}

// Every table of a migrated database is either reviewed as copied or left out.
func TestEveryTableIsDecidedForTheCopy(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset")
	}
	db, _ := clustertest.ScratchDB(t)
	got := tablesOf(t, db, `SELECT c::text FROM cluster_copy_tables() c`)
	want := append([]string(nil), copyReviewed...)
	sort.Strings(want)
	if missing, extra := diff(want, got); len(missing)+len(extra) > 0 {
		t.Fatalf("the copy's tables changed: copied and not reviewed %v (review them: copyReviewed, or "+
			"cluster_copy_excluded() for a secret or a machine's own); reviewed and not copied %v", extra, missing)
	}
}

func tablesOf(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
