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
  ["/library", { topic: "library" }],
  ["/library/:libraryId", { topic: "library" }],
  ["/series", { topic: "series" }],
  ["/series/:id", { topic: "series", anchor: "series-page" }],
  ["/videos/:videoId", { topic: "videos", anchor: "video-page" }],
  ["/videos/:videoId/watch", { topic: "videos", anchor: "play" }],
  ["/search", { topic: "search" }],
  ["/guide", { topic: "watch-tv", anchor: "guide" }],
  ["/live/:number", { topic: "watch-tv", anchor: "player" }],
  ["/queue", { topic: "processing" }],
  ["/queue/items/:kind/:id", { topic: "processing", anchor: "item-history" }],
  ["/queue/reports", { topic: "processing", anchor: "reports" }],
  ["/queue/:stage", { topic: "processing", anchor: "queue-page" }],
  ["/transcription", { topic: "transcription" }],
  ["/settings/rules", { topic: "rules" }],
  ["/settings/sessions", { topic: "settings", anchor: "sessions-tab" }],
  ["/settings/appearance", { topic: "settings", anchor: "appearance-tab" }],
  ["/settings/privacy", { topic: "settings", anchor: "privacy-tab" }],
  ["/settings/models", { topic: "settings", anchor: "models-tab" }],
  ["/settings/about", { topic: "settings", anchor: "about-tab" }],
  ["/settings/*", { topic: "settings" }],
  ["/account", { topic: "account", anchor: "profile" }],
  ["/account/devices", { topic: "account", anchor: "devices" }],
  ["/account/sessions", { topic: "account", anchor: "sessions" }],
  ["/account/tokens", { topic: "account", anchor: "api-tokens" }],
  ["/account/history", { topic: "account", anchor: "watch-history" }],
  ["/billing", { topic: "cloud", anchor: "billing" }],
  ["/cloud/status", { topic: "cloud", anchor: "cloud-status" }],
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
  "awaiting_review",
  "stale_graph",
] as const;

export function causeHelp(cause: string | null | undefined): HelpRef {
  const known = (KNOWN_CAUSES as readonly string[]).includes(cause ?? "");
  return { topic: "failures", anchor: known ? codeAnchor(cause!) : "other" };
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
 * The queue-health failure classes (Epic 40, "Failing items": transient,
 * dependency:model:<section>, dependency:share:<root>, resource:disk,
 * resource:shm, item_permanent, unknown). Epic 40 adds an entry per class
 * here, keyed by the class without its detail ("dependency:model"), with its
 * section in the failures topic; until then a class links to the general
 * entry, and its causes keep their own entries (causeHelp).
 */
export const FAILURE_CLASS_HELP: ReadonlyMap<string, HelpRef> = new Map();

export function failureClassHelp(cls: string): HelpRef {
  const parts = cls.split(":");
  for (let n = parts.length; n > 0; n--) {
    const hit = FAILURE_CLASS_HELP.get(parts.slice(0, n).join(":"));
    if (hit) return hit;
  }
  return causeHelp(cls);
}
