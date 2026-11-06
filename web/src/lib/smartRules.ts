// Epic 56 story 56.09: a smart playlist's rules in words ("Unwatched · Speaker: X ·
// Newest first · up to 50"), and the grid address that reopens them (Edit rules).
import type { SmartQuery } from "./me";

type T = (key: string, vars?: Record<string, string>) => string;

/** The rules as phrases in the reader's language. */
export function rulesInWords(q: SmartQuery, t: T, n: (x: number) => string): string[] {
  const out: string[] = [];
  const f = q.filters ?? {};
  if (f.watched === "unwatched") out.push(t("playlists.smart.rule.unwatched"));
  else if (f.watched === "in_progress") out.push(t("playlists.smart.rule.inProgress"));
  else if (f.watched === "watched") out.push(t("playlists.smart.rule.watched"));
  if (f.speaker) out.push(t("playlists.smart.rule.speaker", { v: f.speaker }));
  if (f.language) out.push(t("playlists.smart.rule.language", { v: f.language }));
  if (f.year) out.push(t("playlists.smart.rule.year", { v: f.year }));
  if (f.q) out.push(t("playlists.smart.rule.search", { v: f.q }));
  if (f.tag) out.push(t("playlists.smart.rule.topic"));
  for (const [k, v] of Object.entries(f)) {
    if (["watched", "speaker", "language", "year", "q", "tag"].includes(k)) continue;
    out.push(t("playlists.smart.rule.other", { k, v }));
  }
  const sort = q.sort || "created_at";
  const asc = q.order === "asc";
  if (sort === "title") out.push(t(asc ? "playlists.smart.rule.az" : "playlists.smart.rule.za"));
  else if (sort === "created_at" || sort === "published_at")
    out.push(t(asc ? "playlists.smart.rule.oldest" : "playlists.smart.rule.newest"));
  else out.push(t("playlists.smart.rule.sorted"));
  out.push(t("playlists.smart.limit", { n: n(q.limit) }));
  if (q.libraries == null) out.push(t("playlists.smart.allLibraries"));
  return out;
}

/** The grid that shows the rules, and saves them back to playlistId. */
export function editRulesPath(q: SmartQuery, playlistId: string): string {
  const qs = new URLSearchParams(q.filters ?? {});
  if (q.sort) qs.set("sort", q.sort);
  if (q.order) qs.set("order", q.order);
  qs.set("smartPlaylist", playlistId);
  const lib = q.libraries && q.libraries.length === 1 ? q.libraries[0] : null;
  return lib ? `/library/${encodeURIComponent(lib)}?tab=browse&${qs}` : `/videos?${qs}`;
}
