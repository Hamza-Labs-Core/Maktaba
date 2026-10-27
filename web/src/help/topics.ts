// The help topics (Epic 41): one per feature, grouped for the /help index.
// Each topic has web/src/help/content/<locale>/<id>.md and the catalog
// strings help.topic.<id>.title / .summary; help.coverage.test.ts holds
// all three to this list.

export const HELP_CATEGORIES = [
  { id: "start", topics: ["getting-started", "mac-app"] },
  {
    id: "apps",
    topics: ["apple-apps", "apple-tv", "android-apps", "android-tv", "desktop-player"],
  },
  {
    id: "watch",
    topics: ["library", "series", "playlists", "videos", "search", "watch-tv", "big-screen"],
  },
  { id: "you", topics: ["account", "settings", "updates"] },
  { id: "processing", topics: ["processing", "stages", "failures", "transcription", "metadata"] },
  {
    id: "tv",
    topics: ["channels", "filler", "live-tv-admin", "tv-changes", "remote-tv", "simulcast"],
  },
  {
    id: "admin",
    topics: [
      "users",
      "library-management",
      "library-access",
      "machines",
      "system-health",
      "logs",
      "audit-log",
      "analytics",
      "cloud",
      "storage",
    ],
  },
  { id: "connections", topics: ["extensions", "plex", "dizquetv"] },
  { id: "rules", topics: ["rules"] },
] as const;

export type HelpCategoryId = (typeof HELP_CATEGORIES)[number]["id"];
export type HelpTopicId = (typeof HELP_CATEGORIES)[number]["topics"][number];

export const HELP_TOPICS: readonly HelpTopicId[] = HELP_CATEGORIES.flatMap((c) => c.topics);

const TOPIC_SET: ReadonlySet<string> = new Set(HELP_TOPICS);

export function isHelpTopic(id: string): id is HelpTopicId {
  return TOPIC_SET.has(id);
}

/** The five sections every topic has, with the same anchors in every language. */
export const STANDARD_ANCHORS = [
  "what-it-is",
  "how-to-use",
  "states",
  "faq",
  "troubleshooting",
] as const;
