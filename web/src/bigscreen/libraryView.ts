// Epic 58 story 58.08 (Epic 56 §6.4 on the big screen) — a library's view, pure.
//
// A library screen is /big/library/:id?tab=…&show=…&sort=…&<filters>. The URL
// holds the whole view, so Back, a reload and Play agree on what is shown:
//
//   - tab: recommended (the default), library, collections, playlists, categories
//   - show (Library tab): videos (the default) or series (GET /api/videos?group=series)
//   - sort: one of SORTS (videos) or SERIES_SORTS (series)
//   - the filters: watched, language, speaker, tag, year, letter (the rail)
//
// Play and Shuffle in the header play the Library tab's current view on the
// server queue (story 56.01): a `library` source with the view's filters and sort.

export const TABS = ["recommended", "library", "collections", "playlists", "categories"] as const;
export type LibraryTab = (typeof TABS)[number];

export type Show = "videos" | "series";

/** The Library tab's sorts of videos: the label key, GET /api/videos's sort and order. */
export const SORTS = {
  newest: { key: "library.sort.newest", sort: "created_at", order: "desc" },
  oldest: { key: "library.sort.oldest", sort: "created_at", order: "asc" },
  az: { key: "library.sort.az", sort: "title", order: "asc" },
  za: { key: "library.sort.za", sort: "title", order: "desc" },
  longest: { key: "library.sort.longest", sort: "duration_sec", order: "desc" },
  shortest: { key: "library.sort.shortest", sort: "duration_sec", order: "asc" },
  published: { key: "library.sort.published", sort: "published_at", order: "desc" },
  plays: { key: "library.sort.plays", sort: "plays", order: "desc" },
} as const;
export type SortId = keyof typeof SORTS;
export const SORT_IDS = Object.keys(SORTS) as SortId[];

/** The sorts of a series view (GET /api/videos?group=series's own). */
export const SERIES_SORTS = {
  newest: { key: "library.sort.newest", group: "newest" },
  az: { key: "library.sort.az", group: "name" },
  recent: { key: "library.sort.watchedRecently", group: "recent" },
} as const;
export type SeriesSortId = keyof typeof SERIES_SORTS;
export const SERIES_SORT_IDS = Object.keys(SERIES_SORTS) as SeriesSortId[];

/** The filters the view keeps (each is GET /api/videos's own key). */
export const FILTER_KEYS = ["watched", "language", "speaker", "tag", "year", "letter"] as const;
export type FilterKey = (typeof FILTER_KEYS)[number];

export const WATCHED = ["unwatched", "in_progress", "watched"] as const;

export interface LibraryView {
  tab: LibraryTab;
  show: Show;
  sort: SortId | SeriesSortId;
  filters: Partial<Record<FilterKey, string>>;
}

export function readView(params: URLSearchParams): LibraryView {
  const tab = (TABS as readonly string[]).includes(params.get("tab") ?? "")
    ? (params.get("tab") as LibraryTab)
    : "recommended";
  const show: Show = params.get("show") === "series" ? "series" : "videos";
  const rawSort = params.get("sort") ?? "";
  const sorts: readonly string[] = show === "series" ? SERIES_SORT_IDS : SORT_IDS;
  const sort = (sorts.includes(rawSort) ? rawSort : "newest") as SortId | SeriesSortId;
  const filters: LibraryView["filters"] = {};
  for (const k of FILTER_KEYS) {
    const v = params.get(k);
    if (!v) continue;
    // the rail's letter is one character (a letter, or # for the rest)
    if (k === "letter" && [...v].length !== 1) continue;
    if (k === "watched" && !(WATCHED as readonly string[]).includes(v)) continue;
    filters[k] = v;
  }
  return { tab, show, sort, filters };
}

/** The view as the screen's query string (defaults left out). */
export function writeView(v: LibraryView): string {
  const q = new URLSearchParams();
  if (v.tab !== "recommended") q.set("tab", v.tab);
  if (v.show !== "videos") q.set("show", v.show);
  if (v.sort !== "newest") q.set("sort", v.sort);
  for (const k of FILTER_KEYS) {
    const f = v.filters[k];
    if (f) q.set(k, f);
  }
  return q.toString();
}

/** How many filters are on, the letter not counted (the rail shows its own). */
export function filterCount(v: LibraryView): number {
  return FILTER_KEYS.filter((k) => k !== "letter" && v.filters[k]).length;
}

/** GET /api/videos's query for one page of the view. */
export function videosQuery(libraryId: string, v: LibraryView, limit: number): URLSearchParams {
  const q = new URLSearchParams({ library: libraryId });
  if (v.show === "series") {
    q.set("group", "series");
    q.set("sort", SERIES_SORTS[v.sort as SeriesSortId]?.group ?? "newest");
  } else {
    const s = SORTS[v.sort as SortId] ?? SORTS.newest;
    q.set("sort", s.sort);
    q.set("order", s.order);
  }
  for (const k of FILTER_KEYS) {
    const f = v.filters[k];
    if (f) q.set(k, f);
  }
  q.set("limit", String(limit));
  return q;
}

/**
 * The queue source's filters for Play and Shuffle: the view's filters and its
 * order. A series view plays its videos in the nearest videos order (A–Z by
 * title for A–Z, newest added otherwise), as the Android TV app does.
 */
export function queueFilters(v: LibraryView): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of FILTER_KEYS) {
    const f = v.filters[k];
    if (f) out[k] = f;
  }
  if (v.show === "series") {
    const az = v.sort === "az";
    out.sort = az ? "title" : "created_at";
    out.order = az ? "asc" : "desc";
  } else {
    const s = SORTS[v.sort as SortId] ?? SORTS.newest;
    out.sort = s.sort;
    out.order = s.order;
  }
  return out;
}

const ARABIC = /^[؀-ۿ]$/;
const LATIN = /^[A-Za-z]$/;

/**
 * The letter rail's order: the letters the library has (GET /api/videos/facets),
 * the reader's script first: ا…ي then A…Z in Arabic, A…Z then ا…ي in English;
 * anything else (digits, #) last. Arabic letters follow the alphabet's order,
 * not code points (ا is U+0627, ب U+0628, but أ, إ and آ belong with ا).
 */
export function railLetters(letters: readonly string[], locale: "en" | "ar"): string[] {
  const latin = letters.filter((l) => LATIN.test(l)).sort((a, b) => a.localeCompare(b, "en"));
  const arabic = letters
    .filter((l) => ARABIC.test(l))
    .sort((a, b) => arabicRank(a) - arabicRank(b));
  const rest = letters.filter((l) => !LATIN.test(l) && !ARABIC.test(l));
  return locale === "ar" ? [...arabic, ...latin, ...rest] : [...latin, ...arabic, ...rest];
}

const ALPHABET = "اأإآبتثجحخدذرزسشصضطظعغفقكلمنهوي";

function arabicRank(l: string): number {
  const i = ALPHABET.indexOf(l);
  // أ إ آ come right after ا, as the alphabet string has them
  return i < 0 ? 1000 + l.charCodeAt(0) : i;
}

/** Whether Play and Shuffle show in the header on `tab` (Epic 56 §6.4). */
export function headerPlays(tab: LibraryTab): boolean {
  return tab !== "collections" && tab !== "playlists";
}
