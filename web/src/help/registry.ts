// Where the app links into the help (Epic 41). helpLink builds the URL; the
// tables below map pages, rules sections, queue stages, failure causes and
// waiting reasons to their topic and anchor. help.coverage.test.ts fails
// when an entry points at a topic or anchor that does not exist, or a page,
// rules section, cause or waiting reason has no entry.
import { matchPath } from "react-router-dom";
import type { HelpTopicId } from "./topics";

export interface HelpRef {
  topic: HelpTopicId;
  anchor?: string;
}

/** The shareable URL of a topic, or of one of its sections. */
export function helpLink(topic: HelpTopicId, anchor?: string): string {
  return `/help/${topic}${anchor ? `#${anchor}` : ""}`;
}

export const hrefOf = (ref: HelpRef) => helpLink(ref.topic, ref.anchor);

/** A code (`nas_timeout`, `tv_remote`) as an anchor (`nas-timeout`, `tv-remote`). */
export const codeAnchor = (code: string) => code.toLowerCase().replace(/[^a-z0-9]+/g, "-");

/**
 * Every page of App.tsx and its topic, most specific first. A page whose
 * route is missing here fails the coverage test.
 */
export const PAGE_HELP: ReadonlyArray<readonly [path: string, ref: HelpRef]> = [
  ["/login", { topic: "getting-started", anchor: "sign-in" }],
  ["/register", { topic: "getting-started", anchor: "register" }],
  ["/more", { topic: "getting-started", anchor: "find-your-way" }],
  ["/help", { topic: "getting-started" }],
  ["/help/:topic", { topic: "getting-started" }],
  ["/", { topic: "library", anchor: "home" }],
  ["/library", { topic: "library", anchor: "libraries-page" }],
  ["/library/:libraryId", { topic: "library", anchor: "library-page" }],
  ["/videos", { topic: "library", anchor: "all-videos" }],
  ["/series", { topic: "series" }],
  ["/series/:id", { topic: "series", anchor: "series-page" }],
  ["/videos/:videoId", { topic: "videos", anchor: "video-page" }],
  ["/videos/:videoId/watch", { topic: "videos", anchor: "play" }],
  ["/search", { topic: "search" }],
  ["/search/video/:videoId", { topic: "search", anchor: "matches-page" }],
  ["/guide", { topic: "watch-tv", anchor: "guide" }],
  ["/live/:number", { topic: "watch-tv", anchor: "player" }],
  ["/queue", { topic: "processing" }],
  ["/queue/items/:kind/:id", { topic: "processing", anchor: "item-history" }],
  ["/queue/reports", { topic: "processing", anchor: "reports" }],
  ["/queue/failures", { topic: "failures", anchor: "failing-items" }],
  ["/queue/:stage", { topic: "processing", anchor: "queue-page" }],
  ["/transcription", { topic: "transcription" }],
  ["/settings/rules", { topic: "rules" }],
  ["/settings/sessions", { topic: "settings", anchor: "sessions-tab" }],
  ["/settings/appearance", { topic: "settings", anchor: "appearance-tab" }],
  ["/settings/privacy", { topic: "settings", anchor: "privacy-tab" }],
  ["/settings/models", { topic: "settings", anchor: "models-tab" }],
  ["/settings/about", { topic: "settings", anchor: "about-tab" }],
  ["/settings/storage", { topic: "storage" }],
  ["/settings/*", { topic: "settings" }],
  ["/account", { topic: "account", anchor: "profile" }],
  ["/account/devices", { topic: "account", anchor: "devices" }],
  ["/account/sessions", { topic: "account", anchor: "sessions" }],
  ["/account/tokens", { topic: "account", anchor: "api-tokens" }],
  ["/account/history", { topic: "account", anchor: "watch-history" }],
  ["/pair", { topic: "account", anchor: "devices" }],
  ["/billing", { topic: "cloud", anchor: "billing" }],
  ["/settings/subscription", { topic: "cloud", anchor: "billing" }],
  ["/cloud/status", { topic: "cloud", anchor: "cloud-status" }],
  ["/admin/libraries", { topic: "library-management" }],
  ["/admin/users", { topic: "users" }],
  ["/admin/library-acl", { topic: "library-access" }],
  ["/admin/extensions", { topic: "extensions" }],
  ["/admin/enrichment", { topic: "metadata", anchor: "review-queue" }],
  ["/admin/channels", { topic: "channels" }],
  ["/admin/filler", { topic: "filler" }],
  ["/admin/live-tv", { topic: "live-tv-admin" }],
  ["/admin/live-tv/changes", { topic: "tv-changes" }],
  ["/admin/live-tv/changes/:id", { topic: "tv-changes", anchor: "change-page" }],
  ["/admin/analytics", { topic: "analytics" }],
  ["/admin/audit", { topic: "audit-log" }],
  ["/admin/health", { topic: "system-health" }],
  ["/admin/machines", { topic: "machines" }],
  ["/admin/updates", { topic: "updates" }],
  ["/admin/logs", { topic: "logs" }],
  ["/admin/devices", { topic: "cloud", anchor: "devices" }],
  ["*", { topic: "getting-started", anchor: "find-your-way" }],
];

/** The help for the page at pathname (null for the help pages themselves). */
export function pageHelp(pathname: string): HelpRef | null {
  if (pathname === "/help" || pathname.startsWith("/help/")) return null;
  for (const [path, ref] of PAGE_HELP) {
    if (path === "*") continue;
    if (matchPath({ path, end: true }, pathname)) return ref;
  }
  return null;
}

/**
 * Rules sections whose help lives in the rules topic under their own
 * anchor (codeAnchor(key)). Each field links to its section's entry.
 */
export function ruleHelp(sectionKey: string): HelpRef {
  return { topic: "rules", anchor: codeAnchor(sectionKey) };
}

/** A queue stage's entry in the processing steps. */
export function stageHelp(stage: string): HelpRef {
  return { topic: "stages", anchor: codeAnchor(stage) };
}

/**
 * The failure causes with an entry of their own (pipeline engine/causes.py
 * and the API's pseudo-causes); every other cause is a generated
 * "kind:message" and links to the general entry.
 */
export const KNOWN_CAUSES = [
  "nas_timeout",
  "nas_invalid_argument",
  "nas_partial_read",
  "file_missing",
  "ffmpeg_decode",
  "youtube_bot_check",
  "youtube_unavailable",
  "plex_unreachable",
  "plex_auth",
  "dizquetv_unreachable",
  "readback_mismatch",
  "no_handler",
  "graph_violation",
  "db_error",
  "shm_full",
  "disk_full",
  "permission_denied",
  "ffmpeg_rc",
  "model_unavailable",
  "awaiting_review",
  "stale_graph",
] as const;

export function causeHelp(cause: string | null | undefined): HelpRef {
  const c = cause ?? "";
  // "model_unavailable: …" is a generated cause of a known kind
  const known = KNOWN_CAUSES.find((k) => c === k || c.startsWith(`${k}:`));
  return { topic: "failures", anchor: known ? codeAnchor(known) : "other" };
}

/** Why an idle queue waits (api queues/types.go WaitingReason). */
export const WAITING_REASONS = [
  "disabled",
  "not_configured",
  "paused_all",
  "paused",
  "awaiting_review",
  "no_worker",
  "pool_full",
  "debouncing",
  "backoff",
  "service_retrying",
  "upstream",
  "idle",
] as const;

export function waitingHelp(reason: string): HelpRef {
  return { topic: "processing", anchor: `waiting-${codeAnchor(reason)}` };
}

/**
 * The queue-health failure classes (Epic 40, engine/failure_classes.py),
 * keyed by the class without its detail: "dependency:model:tag" finds
 * "dependency:model". A class with no entry links to its cause's entry.
 */
export const FAILURE_CLASS_HELP: ReadonlyMap<string, HelpRef> = new Map([
  ["transient", { topic: "failures", anchor: "class-transient" }],
  ["dependency:model", { topic: "failures", anchor: "class-dependency-model" }],
  ["dependency:share", { topic: "failures", anchor: "class-dependency-share" }],
  ["resource:disk", { topic: "failures", anchor: "class-resource-disk" }],
  ["resource:shm", { topic: "failures", anchor: "class-resource-shm" }],
  ["item_permanent", { topic: "failures", anchor: "class-item-permanent" }],
  ["unknown", { topic: "failures", anchor: "class-unknown" }],
  // the Failing items page filters by the class without its detail
  ["dependency", { topic: "failures", anchor: "classes" }],
  ["resource", { topic: "failures", anchor: "classes" }],
]);

export function failureClassHelp(cls: string): HelpRef {
  const parts = cls.split(":");
  for (let n = parts.length; n > 0; n--) {
    const hit = FAILURE_CLASS_HELP.get(parts.slice(0, n).join(":"));
    if (hit) return hit;
  }
  return causeHelp(cls);
}

/** A status of the Failing items page (Epic 40). */
export function failureStatusHelp(status: string): HelpRef {
  const known = ["needs_you", "failing", "waiting", "retrying", "ignored"].includes(status);
  return { topic: "failures", anchor: known ? `status-${codeAnchor(status)}` : "statuses" };
}

/** An action of the Failing items page. */
export function failureActionHelp(action: string): HelpRef {
  return { topic: "failures", anchor: `action-${codeAnchor(action)}` };
}

/** A queue tile's health note: failing, waiting, or the Needs-you badge. */
export function tileHealthHelp(what: "failing" | "waiting" | "needs_you"): HelpRef {
  return { topic: "failures", anchor: what === "needs_you" ? "needs-you" : `tile-${what}` };
}

/**
 * The kinds of part the System health page shows (api
 * handlers/systemstatus/types.go Kinds); each has its entry
 * system-health#item-<kind>. An unknown kind links to the list of parts.
 */
export const SYSTEM_HEALTH_KINDS = [
  "api",
  "postgres",
  "streaming",
  "pipeline",
  "process",
  "workers",
  "worker",
  "streaming_host",
  "dependency",
  "plex_livetv",
  "queues",
  "tv_channels",
  "tv_change",
  "tv_slot",
  "tv_tuner",
] as const;

export function systemHealthHelp(kind: string): HelpRef {
  const known = (SYSTEM_HEALTH_KINDS as readonly string[]).includes(kind);
  return { topic: "system-health", anchor: known ? `item-${codeAnchor(kind)}` : "items" };
}

/**
 * The reasons an update stops with (Admin -> Updates: a machine's last attempt, a run, a
 * machine's part of a run; web i18n updates.code.*) and their entry in the updates topic,
 * one per kind of failure. A check of the release (verify_*) links to its signature entry;
 * a reason a newer server sends, to the general one.
 */
export const UPDATE_CODE_HELP: ReadonlyMap<string, string> = new Map([
  // updates#rolled-back
  ["admin", "rolled-back"],
  ["admin_rollback", "rolled-back"],
  ["boot_loop", "rolled-back"],
  ["did_not_start", "rolled-back"],
  ["did_not_stay_up", "rolled-back"],
  ["interrupted", "rolled-back"],
  ["launcher_boot_loop", "rolled-back"],
  ["launcher_pending_timeout", "rolled-back"],
  ["launcher_refused", "rolled-back"],
  ["pending_timeout", "rolled-back"],
  ["restarts", "rolled-back"],
  ["rollback_failed", "rolled-back"],
  ["rollback_loop", "rolled-back"],
  ["rolled_back", "rolled-back"],
  ["start_failed", "rolled-back"],
  ["start_interrupted", "rolled-back"],
  ["start_timeout", "rolled-back"],
  ["switch_failed", "rolled-back"],
  ["watch_timeout", "rolled-back"],
  // updates#verify-failed
  ["checksums_changed", "verify-failed"],
  ["payload_invalid", "verify-failed"],
  ["version_mismatch", "verify-failed"],
  ["trust_root", "verify-failed"],
  ["no_trust_root", "verify-failed"],
  // updates#replay
  ["verify_replay", "replay"],
  ["verify_downgrade", "replay"],
  ["older_build", "replay"],
  // updates#code-database
  ["database", "code-database"],
  ["db_ahead", "code-database"],
  ["db_dump_failed", "code-database"],
  ["db_migrate_failed", "code-database"],
  ["db_refused_older", "code-database"],
  ["db_restore_failed", "code-database"],
  ["rehearsal_failed", "code-database"],
  ["pg_major_change", "code-database"],
  // updates#code-tv
  ["tv_busy", "code-tv"],
  ["tv_change_pending", "code-tv"],
  ["tv_timeout", "code-tv"],
  ["failed_tv", "code-tv"],
  ["tv_change_cancelled", "code-tv"],
  // updates#code-machine
  ["failed_prepare", "code-machine"],
  ["prepare_failed", "code-machine"],
  ["prepare_refused", "code-machine"],
  ["prepare_timeout", "code-machine"],
  ["apply_failed", "code-machine"],
  ["apply_refused", "code-machine"],
  ["node_health_timeout", "code-machine"],
  ["held", "code-machine"],
  ["home_not_prepared", "code-machine"],
  ["home_timeout", "code-machine"],
  ["window_over", "code-machine"],
  ["outside_window", "code-machine"],
  ["convert_refused", "code-machine"],
  ["convert_timeout", "code-machine"],
  ["switchover", "code-machine"],
  ["switchover_open", "code-machine"],
  ["journal_lost", "code-machine"],
  // updates#code-desktop-app
  ["app_did_not_install", "code-desktop-app"],
  ["app_install_failed", "code-desktop-app"],
  ["app_management", "code-desktop-app"],
  ["app_not_started", "code-desktop-app"],
  ["app_order_gone", "code-desktop-app"],
  ["app_request_failed", "code-desktop-app"],
  ["asset_not_found", "code-desktop-app"],
  ["check_failed", "code-desktop-app"],
  ["feed_invalid", "code-desktop-app"],
  ["feed_not_found", "code-desktop-app"],
  ["installer_exit", "code-desktop-app"],
  ["installer_not_finished", "code-desktop-app"],
  ["local_build", "code-desktop-app"],
  ["login_item_unsigned", "code-desktop-app"],
  ["needs_admin", "code-desktop-app"],
  ["no_app", "code-desktop-app"],
  ["no_app_update", "code-desktop-app"],
  ["no_network", "code-desktop-app"],
  ["not_writable", "code-desktop-app"],
  ["previous_app_not_restored", "code-desktop-app"],
  ["quarantined", "code-desktop-app"],
  ["relaunch_failed", "code-desktop-app"],
  ["server_stop_failed", "code-desktop-app"],
  ["signature_mismatch", "code-desktop-app"],
  ["translocated", "code-desktop-app"],
  // updates#code-python
  ["venv_failed", "code-python"],
  ["venv_interrupted", "code-python"],
  ["venv_network", "code-python"],
  ["venv_python_unsupported", "code-python"],
  ["no_python", "code-python"],
  ["python_store_stub", "code-python"],
  // updates#code-needs-person
  ["needs_person", "code-needs-person"],
  ["not_self_managed", "code-needs-person"],
  ["orchestrator_unavailable", "code-needs-person"],
  ["old_launcher", "code-needs-person"],
  ["launcher_protocol", "code-needs-person"],
  ["no_runtime", "code-needs-person"],
  ["worker_only", "code-needs-person"],
  // updates#code-download
  ["download_failed", "code-download"],
  ["release_incomplete", "code-download"],
  ["disk_full", "code-download"],
  ["disk_space", "code-download"],
  ["no_release", "code-download"],
  ["withdrawn", "code-download"],
  ["turned_prerelease", "code-download"],
  // updates#run-failed
  ["bad_job", "run-failed"],
  ["cancelled", "run-failed"],
  ["failed", "run-failed"],
  ["refused", "run-failed"],
  ["superseded", "run-failed"],
  ["replaced_by_newer", "run-failed"],
  ["smoke_failed", "run-failed"],
  ["smoke_missing", "run-failed"],
  ["smoke_version", "run-failed"],
  ["install_failed", "run-failed"],
  ["keep_failed", "run-failed"],
  ["no_previous", "run-failed"],
]);

export function updateCodeHelp(code: string | null | undefined): HelpRef {
  const c = code ?? "";
  const anchor =
    UPDATE_CODE_HELP.get(c) ?? (c.startsWith("verify_") ? "verify-failed" : "run-failed");
  return { topic: "updates", anchor };
}
