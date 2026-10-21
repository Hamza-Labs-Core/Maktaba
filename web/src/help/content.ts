// The help topics' Markdown, bundled with the app (Epic 41): one chunk per
// file, fetched the first time it is read. A topic missing in the reader's
// language falls back to English.
import type { Locale } from "../lib/i18n";
import type { HelpTopicId } from "./topics";

const FILES = import.meta.glob<string>("./content/*/*.md", {
  query: "?raw",
  import: "default",
});

const cache = new Map<string, Promise<string | null>>();

function load(locale: Locale, id: string): Promise<string | null> {
  const path = `./content/${locale}/${id}.md`;
  let hit = cache.get(path);
  if (!hit) {
    const loader = FILES[path];
    hit = loader ? loader().catch(() => null) : Promise.resolve(null);
    cache.set(path, hit);
  }
  return hit;
}

/** The topic's Markdown in locale (English when that language has none). */
export async function loadTopic(locale: Locale, id: HelpTopicId): Promise<string | null> {
  return (await load(locale, id)) ?? (locale === "en" ? null : load("en", id));
}

/** Every topic in locale, for search. */
export async function loadAll(
  locale: Locale,
  ids: readonly HelpTopicId[]
): Promise<Array<{ id: HelpTopicId; src: string }>> {
  const all = await Promise.all(ids.map(async (id) => ({ id, src: await loadTopic(locale, id) })));
  return all.filter((t): t is { id: HelpTopicId; src: string } => t.src !== null);
}
