// What the Search page and the per-video matches page share: the saved
// search contract (POST /api/search, GET /api/search/results/{sid},
// GET /api/search/passages), snippet rendering and the URLs between them.
import type { ReactNode } from "react";
import { ApiError } from "../lib/api";

export type Mode = "hybrid" | "fts" | "semantic";
export const MODE_KEY = "mkt:search:mode";
const MODES: readonly string[] = ["hybrid", "fts", "semantic"];
export const asMode = (m: unknown): Mode => (MODES.includes(m as string) ? (m as Mode) : "hybrid");
/** The mode in a URL, or null when it has none (or an unknown one). */
export const urlMode = (m: string | null): Mode | null =>
  m !== null && MODES.includes(m) ? (m as Mode) : null;

export function readMode(): Mode {
  try {
    const m = localStorage.getItem(MODE_KEY);
    if (m === "hybrid" || m === "fts" || m === "semantic") return m;
  } catch {
    /* ignored */
  }
  return "hybrid";
}

/** A 410 answer for a search whose saved results are gone. */
export const PROBLEM_SEARCH_EXPIRED = "https://maktaba.tv/problems/search-expired";
export const isSearchExpired = (e: unknown): boolean =>
  e instanceof ApiError && e.status === 410 && e.problem.type === PROBLEM_SEARCH_EXPIRED;

export interface VideoCard {
  video_id: string;
  title: string;
  duration_sec?: number;
  library_id?: string;
  library_name?: string;
  poster_url?: string;
}
export interface TitleVideo extends VideoCard {
  score?: number;
  in_transcripts?: boolean;
  title_snippet?: string;
}
export interface Passage {
  segment_id: number;
  start_sec: number;
  end_sec: number;
  snippet: string | null;
  score?: number;
}
export interface TranscriptVideo extends VideoCard {
  best_score?: number;
  matches: number;
  passages: Passage[] | null;
  in_title?: boolean;
}
export interface SearchResponse {
  search_id?: string;
  expires_at?: string;
  q?: string;
  mode?: string;
  page_size?: number;
  passages_page_size?: number;
  videos?: TitleVideo[] | null;
  videos_total?: number;
  transcript_videos?: TranscriptVideo[] | null;
  transcript_total?: number;
  /** The order the lists are in (story 49.8). */
  sort?: string;
  /** Counts of the videos found, with the first page only (story 49.8). */
  facets?: Facets | null;
  /** The query with its rare or unknown words corrected, when few videos were found. */
  did_you_mean?: string;
}

// Story 49.10: the search page's sort and facet filters (story 49.8's API).
export type Sort = "relevance" | "newest" | "matches";
export const SORTS: readonly Sort[] = ["relevance", "newest", "matches"];
/** The sort in a URL; relevance when it has none (or an unknown one). */
export const urlSort = (v: string | null | undefined): Sort =>
  SORTS.includes(v as Sort) ? (v as Sort) : "relevance";

/** A date or seconds range, as POST /api/search filters take it. */
export interface Range<T> {
  gte?: T;
  lte?: T;
  lt?: T;
}
/** The request filter a year or duration bucket stands for. */
export interface BucketFilter {
  date?: Range<string>;
  duration_sec?: Range<number>;
}
export interface Bucket {
  value: string;
  /** A library's name (library facet). */
  label?: string;
  count: number;
  filter?: BucketFilter;
}
export interface Facets {
  library?: Bucket[] | null;
  language?: Bucket[] | null;
  speaker?: Bucket[] | null;
  year?: Bucket[] | null;
  duration?: Bucket[] | null;
  videos?: number;
}
export type FacetName = "library" | "language" | "speaker" | "year" | "duration";
export const FACETS: readonly FacetName[] = ["library", "language", "speaker", "year", "duration"];
/** Duration bands, as the server names them (api/internal/handlers/search/facets.go). */
export const DURATION_BANDS: Record<string, Range<number>> = {
  under_5m: { gte: 0, lt: 300 },
  "5m_20m": { gte: 300, lt: 1200 },
  "20m_60m": { gte: 1200, lt: 3600 },
  over_60m: { gte: 3600 },
};

/**
 * The facet buckets chosen: libraries, languages and speakers are lists (any of them),
 * a year and a duration band one each (the request has one date and one duration range).
 */
export interface FacetSel {
  library: string[];
  language: string[];
  speaker: string[];
  year?: string;
  duration?: string;
}
export const noSel = (): FacetSel => ({ library: [], language: [], speaker: [] });
export const selCount = (s: FacetSel | undefined) =>
  s
    ? s.library.length +
      s.language.length +
      s.speaker.length +
      (s.year ? 1 : 0) +
      (s.duration ? 1 : 0)
    : 0;

// URL parameters of the facet filters; a list repeats its parameter.
const SEL_PARAMS = { library: "lib", language: "lang", speaker: "spk" } as const;
const uniq = (v: string[]) => [...new Set(v.filter((x) => x.trim() !== ""))];

/** The facet filters in a URL. */
export function selOf(p: URLSearchParams): FacetSel {
  const year = p.get("year") ?? "";
  const duration = p.get("dur") ?? "";
  return {
    library: uniq(p.getAll(SEL_PARAMS.library)),
    language: uniq(p.getAll(SEL_PARAMS.language)),
    speaker: uniq(p.getAll(SEL_PARAMS.speaker)),
    ...(/^\d{1,4}$/.test(year) ? { year } : {}),
    ...(duration in DURATION_BANDS ? { duration } : {}),
  };
}

/** The date range of a year, as the server's year bucket gives it. */
export const yearRange = (y: string): Range<string> => ({
  gte: `${y.padStart(4, "0")}-01-01T00:00:00.000Z`,
  lt: `${String(Number(y) + 1).padStart(4, "0")}-01-01T00:00:00.000Z`,
});

/**
 * The POST /api/search filters of a selection (undefined when there are none). A year or
 * duration takes its bucket's own filter when the facets on screen have it, else the
 * range the server gives that bucket.
 */
export function requestFilters(
  sel: FacetSel | undefined,
  facets?: Facets | null
): Record<string, unknown> | undefined {
  if (!sel || selCount(sel) === 0) return undefined;
  const f: Record<string, unknown> = {};
  if (sel.library.length) f.library_id = sel.library;
  if (sel.language.length) f.language = sel.language;
  if (sel.speaker.length) f.speaker = sel.speaker;
  if (sel.year) {
    const b = listOf(facets?.year).find((x) => x.value === sel.year);
    f.date = b?.filter?.date ?? yearRange(sel.year);
  }
  if (sel.duration) {
    const b = listOf(facets?.duration).find((x) => x.value === sel.duration);
    f.duration_sec = b?.filter?.duration_sec ?? DURATION_BANDS[sel.duration];
  }
  return f;
}

const strings = (v: unknown): string[] =>
  Array.isArray(v) ? uniq(v.filter((x): x is string => typeof x === "string")) : [];
const num = (v: unknown): number | undefined =>
  typeof v === "number" && Number.isFinite(v) ? v : undefined;

/**
 * The facet selection of a search's filters (as a recent or saved search keeps them): the
 * inverse of requestFilters. A date range is a year when it spans one calendar year (as a
 * year bucket's does), a duration range a band when it is one of the bands; any other range
 * (from another client) is left out, as the page cannot show it.
 */
export function selOfFilters(f: unknown): FacetSel {
  const sel = noSel();
  if (!f || typeof f !== "object") return sel;
  const o = f as Record<string, unknown>;
  sel.library = strings(o.library_id);
  sel.language = strings(o.language);
  sel.speaker = strings(o.speaker);
  const date = o.date as Range<string> | null | undefined;
  if (date && typeof date === "object" && typeof date.gte === "string") {
    const y = /^(\d{4})-01-01/.exec(date.gte)?.[1];
    if (y) {
      const next = yearRange(String(Number(y))).lt!.slice(0, 10);
      const end = typeof date.lt === "string" ? date.lt : "";
      if (end.slice(0, 10) === next && date.lte === undefined) sel.year = String(Number(y));
    }
  }
  const dur = o.duration_sec as Range<number> | null | undefined;
  if (dur && typeof dur === "object") {
    const gte = num(dur.gte) ?? 0;
    const lt = num(dur.lt);
    for (const [band, r] of Object.entries(DURATION_BANDS))
      if ((r.gte ?? 0) === gte && r.lt === lt && dur.lte === undefined) sel.duration = band;
  }
  return sel;
}

/** How a recent or saved search was asked: its order and filters (old ones have neither). */
export interface AskedState {
  sort?: string;
  filters?: Record<string, unknown> | null;
}

/**
 * A saved search (GET /api/search/saved): `query` is the POST /api/search body it was saved
 * with, so it keeps the order and filters; an older one has neither.
 */
export interface SavedSearch {
  id: string;
  name?: string;
  query?: ({ q?: string; mode?: string } & AskedState) | null;
  created_at?: string;
  /** The shape a home before 49.10's follow-up was mocked with (no `query`). */
  q?: string;
  mode?: string;
}

/** A saved search's words, mode and how it was asked, from whichever shape it has. */
export function savedOf(s: SavedSearch): { q: string; mode: string; name: string } & AskedState {
  const query = s.query && typeof s.query === "object" ? s.query : null;
  const q = (typeof query?.q === "string" ? query.q : (s.q ?? s.name ?? "")).trim();
  return {
    q,
    mode: query?.mode ?? s.mode ?? "hybrid",
    name: (s.name ?? "").trim() || q,
    sort: typeof query?.sort === "string" ? query.sort : undefined,
    filters: query?.filters && typeof query.filters === "object" ? query.filters : undefined,
  };
}

/** Toggles one bucket: on a list it joins or leaves; a year or duration replaces the last. */
export function toggleSel(sel: FacetSel, facet: FacetName, value: string): FacetSel {
  if (facet === "year" || facet === "duration") {
    const next = { ...sel };
    if (sel[facet] === value) delete next[facet];
    else next[facet] = value;
    return next;
  }
  const list = sel[facet];
  return {
    ...sel,
    [facet]: list.includes(value) ? list.filter((v) => v !== value) : [...list, value],
  };
}

export const selHas = (sel: FacetSel, facet: FacetName, value: string) =>
  facet === "year" || facet === "duration" ? sel[facet] === value : sel[facet].includes(value);

/** The facets of an answer, or null (a home before story 49.8 sends none). */
export const facetsOf = (res: SearchResponse | null | undefined): Facets | null =>
  res?.facets && typeof res.facets === "object" ? res.facets : null;

/** An answer's corrected query, or "" when it has none or repeats the query. */
export const didYouMeanOf = (res: SearchResponse | null | undefined, query: string): string =>
  typeof res?.did_you_mean === "string" && res.did_you_mean.trim() !== query.trim()
    ? res.did_you_mean.trim()
    : "";

/** What a saved search was asked and answered beyond its pages, kept per sid for a reload. */
export interface SearchHelp {
  sort: Sort;
  sel: FacetSel;
  facets: Facets | null;
  didYouMean: string;
}
const HELP_KEY = "mkt:search:help";
export function rememberHelp(sid: string, help: SearchHelp) {
  if (!sid) return;
  try {
    const all = JSON.parse(sessionStorage.getItem(HELP_KEY) || "{}") as Record<string, SearchHelp>;
    const keys = Object.keys(all);
    if (keys.length > 50) delete all[keys[0]!];
    all[sid] = help;
    sessionStorage.setItem(HELP_KEY, JSON.stringify(all));
  } catch {
    /* ignored */
  }
}
export function recallHelp(sid: string): SearchHelp | null {
  try {
    const all = JSON.parse(sessionStorage.getItem(HELP_KEY) || "{}") as Record<string, SearchHelp>;
    const h = all[sid];
    if (h && typeof h === "object" && h.sel && typeof h.sel === "object") {
      return {
        sort: urlSort(h.sort),
        sel: {
          library: Array.isArray(h.sel.library) ? h.sel.library : [],
          language: Array.isArray(h.sel.language) ? h.sel.language : [],
          speaker: Array.isArray(h.sel.speaker) ? h.sel.speaker : [],
          ...(typeof h.sel.year === "string" ? { year: h.sel.year } : {}),
          ...(typeof h.sel.duration === "string" ? { duration: h.sel.duration } : {}),
        },
        facets: h.facets && typeof h.facets === "object" ? h.facets : null,
        didYouMean: typeof h.didYouMean === "string" ? h.didYouMean : "",
      };
    }
  } catch {
    /* ignored */
  }
  return null;
}

export interface ResultsPage<T> {
  search_id: string;
  q?: string;
  mode?: string;
  offset: number;
  limit: number;
  total: number;
  items: T[] | null;
  /** The order its pages are in (a search kept before 49.10's follow-up has none). */
  sort?: string;
  /** Its facets and did-you-mean, with facets=1. */
  facets?: Facets | null;
  did_you_mean?: string;
}
export interface PassagesPage {
  search_id: string;
  q?: string;
  mode?: string;
  video?: VideoCard;
  in_title?: boolean;
  title_snippet?: string;
  total: number;
  offset: number;
  limit: number;
  items: Passage[] | null;
  times?: number[] | null;
}

export const DEFAULT_PASSAGES_PAGE_SIZE = 20;
/** The server's default page sizes per view (rules page_size_grid / _list). */
export const defaultPageSize = (view: "grid" | "list") => (view === "grid" ? 24 : 20);

/** Non-null items of a list the server may send as null. */
export const listOf = <T,>(v: T[] | null | undefined): T[] =>
  Array.isArray(v) ? v.filter((x): x is T => x != null) : [];

// The page sizes of a saved search, so a reload or the matches page pages
// it the same way the first answer did.
const SIZES_KEY = "mkt:search:sizes";
export interface PageSizes {
  pageSize: number;
  passagesPageSize: number;
}
export function rememberSizes(sid: string, sizes: PageSizes) {
  if (!sid) return;
  try {
    const all = JSON.parse(sessionStorage.getItem(SIZES_KEY) || "{}") as Record<string, PageSizes>;
    const keys = Object.keys(all);
    if (keys.length > 50) delete all[keys[0]!];
    all[sid] = sizes;
    sessionStorage.setItem(SIZES_KEY, JSON.stringify(all));
  } catch {
    /* ignored */
  }
}
export function recallSizes(sid: string): PageSizes | null {
  try {
    const all = JSON.parse(sessionStorage.getItem(SIZES_KEY) || "{}") as Record<string, PageSizes>;
    const s = all[sid];
    if (s && s.pageSize > 0 && s.passagesPageSize > 0) return s;
  } catch {
    /* ignored */
  }
  return null;
}

export interface SearchState {
  q: string;
  mode: Mode;
  sid?: string;
  vp?: number;
  tp?: number;
  /** The order (left out of the URL when relevance). */
  sort?: Sort;
  /** The facet filters. */
  sel?: FacetSel;
}

/** The URL parameters of a search and its pages. */
export function searchParamsOf(s: SearchState): URLSearchParams {
  const p = new URLSearchParams();
  p.set("q", s.q);
  p.set("mode", s.mode);
  if (s.sort && s.sort !== "relevance") p.set("sort", s.sort);
  if (s.sel) {
    for (const k of ["library", "language", "speaker"] as const)
      for (const v of s.sel[k]) p.append(SEL_PARAMS[k], v);
    if (s.sel.year) p.set("year", s.sel.year);
    if (s.sel.duration) p.set("dur", s.sel.duration);
  }
  if (s.sid) p.set("sid", s.sid);
  if (s.vp && s.vp > 1) p.set("vp", String(s.vp));
  if (s.tp && s.tp > 1) p.set("tp", String(s.tp));
  return p;
}

/** The Search page URL of a search and its pages. */
export const searchHref = (s: SearchState) => `/search?${searchParamsOf(s).toString()}`;

/** The matches page of one video in a search. */
export const matchesHref = (videoId: string, s: SearchState) =>
  `/search/video/${encodeURIComponent(videoId)}?${searchParamsOf(s).toString()}`;

/** The player at a passage. */
export const watchHref = (videoId: string, sec: number) =>
  `/videos/${encodeURIComponent(videoId)}/watch?t=${Math.floor(sec)}`;

/** A positive page number from the URL (1 when missing or bad). */
export const pageParam = (v: string | null): number => {
  const n = Number(v);
  return Number.isInteger(n) && n > 0 ? n : 1;
};

export const pageCount = (total: number, size: number) =>
  Math.max(1, Math.ceil(Math.max(0, total) / Math.max(1, size)));

export function ts(s: number): string {
  const m = Math.floor(s / 60);
  const sec = Math.floor(s % 60);
  return `${m}:${sec.toString().padStart(2, "0")}`;
}

// Server emits only <mark>…</mark> wrappers; split on the literal tags
// and keep every other run as a plain (escaped) text node. The whole snippet
// is one dir="auto" run: a dir="auto" span per text piece made each piece its
// own bidi island, so in an LTR page an Arabic title lost the space between a
// plain word and the marked one ("أقم <mark>الصلاة</mark>" read "الصلاةأقم").
export function renderSnippet(snippet: string | null | undefined): ReactNode {
  if (typeof snippet !== "string") return null;
  const parts = snippet.split(/(<mark>|<\/mark>)/);
  const nodes: ReactNode[] = [];
  let inMark = false;
  let key = 0;
  for (const part of parts) {
    if (part === "<mark>") {
      inMark = true;
      continue;
    }
    if (part === "</mark>") {
      inMark = false;
      continue;
    }
    if (part === "") continue;
    nodes.push(inMark ? <mark key={key++}>{part}</mark> : part);
  }
  return <span dir="auto">{nodes}</span>;
}

/** "12 matches" (one match reads "1 match"). */
export function matchesLabel(
  t: (key: string, vars?: Record<string, string | number>) => string,
  n: (value: number) => string,
  count: number
): string {
  return count === 1 ? t("search.matches.one") : t("search.matches", { count: n(count) });
}
