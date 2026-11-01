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
}
export interface ResultsPage<T> {
  search_id: string;
  q?: string;
  mode?: string;
  offset: number;
  limit: number;
  total: number;
  items: T[] | null;
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
}

/** The URL parameters of a search and its pages. */
export function searchParamsOf(s: SearchState): URLSearchParams {
  const p = new URLSearchParams();
  p.set("q", s.q);
  p.set("mode", s.mode);
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
