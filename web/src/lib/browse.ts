// Epic 45 browsing API (every signed-in user; only the libraries they may see):
//
//   GET /api/home                    -> HomeResponse
//   GET /api/libraries/summary       -> { items: LibrarySummary[] }
//   GET /api/videos (extended)       -> lib/LibraryBrowser (the grid)
//   GET /api/libraries/{id}/hub      -> LibraryHub (404: not visible)
//   GET/PUT /api/me/pinned-libraries -> lib/pins.ts
//
// A row is a list of video cards; a card's picture is /api/videos/{id}/poster.
// The readers below are defensive: a missing or malformed list is an empty
// row, so an older server (or a half-built answer) hides rows instead of
// crashing the page.
import { api } from "./api";

export interface VideoCard {
  id: string;
  title?: string | null;
  filename?: string | null;
  library_id?: string;
  duration_sec?: number | null;
  state?: string;
  created_at?: string;
  /** the file's mtime: a download's upload date */
  published_at?: string | null;
  position_sec?: number | null;
  completed?: boolean | null;
  last_watched_at?: string | null;
  plays?: number | null;
  /** On Deck: the series this lesson is the next one of */
  series?: { id: string; name: string } | null;
}

/**
 * Story 45.18: an entry of Recently Added grouped by series: a video, or a
 * series with `count` new lessons (`video` is the newest of them). A video
 * entry may name its series (the only new lesson of it).
 */
export interface AddedEntry {
  kind: "video" | "series";
  video: VideoCard;
  series?: { id: string; name: string } | null;
  count?: number;
}

/**
 * Story 45.21: Home's hero: the newest series (shown from its first lesson)
 * or the lesson an admin picked (`picked`, with its series when it has one).
 */
export interface Featured {
  kind: "series" | "video";
  video: VideoCard;
  series?: { id: string; name: string; episodes?: number } | null;
  picked: boolean;
}

export interface LibraryRef {
  id: string;
  name: string;
}

export interface PinnedRows {
  library: LibraryRef;
  recently_added: VideoCard[];
  continue_watching: VideoCard[];
}

export interface HomeResponse {
  /** the next lesson of each series the user is in the middle of (Phase 2) */
  on_deck: VideoCard[];
  /** the user's favorites, newest first (Phase 2) */
  favorites: VideoCard[];
  continue_watching: VideoCard[];
  recently_added: VideoCard[];
  /** Recently Added grouped by series (Phase 3); empty from an older server */
  recently_added_grouped: AddedEntry[];
  recently_watched: VideoCard[];
  pinned: PinnedRows[];
  /** the hero (Phase 3); null when there is nothing to feature */
  featured: Featured | null;
}

export interface LibrarySummary {
  id: string;
  name: string;
  videos: number;
  duration_sec?: number | null;
  poster_video_ids: string[];
  latest_added_at?: string | null;
  pinned: boolean;
  pin_position?: number | null;
}

export interface HubSeries {
  id: string;
  name: string | null;
  episodes: number;
}

export interface HubYear {
  year: number;
  videos: number;
}

export interface LibraryHub {
  library: { id: string; name: string; videos: number };
  rows: {
    on_deck: VideoCard[];
    continue_watching: VideoCard[];
    recently_added: VideoCard[];
    recently_watched: VideoCard[];
    most_watched: VideoCard[];
    recently_added_grouped: AddedEntry[];
    series: HubSeries[];
    years: HubYear[];
  };
}

const list = <T>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);
const cards = (v: unknown) =>
  list<VideoCard>(v).filter((c) => c && typeof c === "object" && typeof c.id === "string");
const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};
const grouped = (v: unknown): AddedEntry[] =>
  list<AddedEntry>(v).filter(
    (e) =>
      e &&
      (e.kind === "video" || e.kind === "series") &&
      e.video &&
      typeof e.video.id === "string" &&
      (e.kind === "video" || (!!e.series && typeof e.series.id === "string"))
  );

function featuredOf(v: unknown): Featured | null {
  const f = obj(v);
  const video = obj(f.video);
  if ((f.kind !== "series" && f.kind !== "video") || typeof video.id !== "string") return null;
  const series = obj(f.series);
  if (f.kind === "series" && typeof series.id !== "string") return null;
  return {
    kind: f.kind,
    video: video as unknown as VideoCard,
    series: typeof series.id === "string" ? (series as unknown as Featured["series"]) : null,
    picked: f.picked === true,
  };
}

/** The admin's pick for the hero (admins only). */
export async function fetchFeaturedPick(): Promise<string | null> {
  const r = obj(await api.get<unknown>("/api/home/featured"));
  return typeof r.video_id === "string" ? r.video_id : null;
}

/** Picks the lesson Home features; null goes back to the newest series. */
export async function setFeaturedPick(videoId: string | null): Promise<string | null> {
  const r = obj(await api.put<unknown>("/api/home/featured", { video_id: videoId }));
  return typeof r.video_id === "string" ? r.video_id : null;
}

export async function fetchHome(): Promise<HomeResponse> {
  const r = obj(await api.get<unknown>("/api/home"));
  return {
    on_deck: cards(r.on_deck),
    favorites: cards(r.favorites),
    continue_watching: cards(r.continue_watching),
    recently_added: cards(r.recently_added),
    recently_added_grouped: grouped(r.recently_added_grouped),
    featured: featuredOf(r.featured),
    recently_watched: cards(r.recently_watched),
    pinned: list<PinnedRows>(r.pinned)
      .filter((p) => p && p.library && typeof p.library.id === "string")
      .map((p) => ({
        library: p.library,
        recently_added: cards(p.recently_added),
        continue_watching: cards(p.continue_watching),
      })),
  };
}

export async function fetchLibrarySummary(): Promise<LibrarySummary[]> {
  const r = await api.get<{ items: LibrarySummary[] | null }>("/api/libraries/summary");
  return (r?.items ?? []).map((l) => ({
    ...l,
    poster_video_ids: (l.poster_video_ids ?? []).slice(0, 4),
  }));
}

export async function fetchHub(id: string): Promise<LibraryHub> {
  const r = obj(await api.get<unknown>(`/api/libraries/${encodeURIComponent(id)}/hub`));
  const lib = obj(r.library);
  const rows = obj(r.rows);
  return {
    library: {
      id: typeof lib.id === "string" ? lib.id : id,
      name: typeof lib.name === "string" ? lib.name : "",
      videos: typeof lib.videos === "number" ? lib.videos : 0,
    },
    rows: {
      on_deck: cards(rows.on_deck),
      continue_watching: cards(rows.continue_watching),
      recently_added: cards(rows.recently_added),
      recently_watched: cards(rows.recently_watched),
      most_watched: cards(rows.most_watched),
      recently_added_grouped: grouped(rows.recently_added_grouped),
      series: list<HubSeries>(rows.series).filter((s) => s && typeof s.id === "string"),
      years: list<HubYear>(rows.years).filter((y) => y && typeof y.year === "number"),
    },
  };
}

/** How far into a card the viewer is (0..1), or null when there is nothing to show. */
export function watchedFraction(c: VideoCard): number | null {
  const pos = c.position_sec ?? 0;
  const dur = c.duration_sec ?? 0;
  if (c.completed || pos <= 0 || dur <= 0) return null;
  return Math.min(1, pos / dur);
}

/**
 * The video list's URL for a query string. The caller's favorites are a user's
 * own table, which a machine's copy leaves out: ?favorite=true is read on the
 * home's /api/me/videos (the same list); every other query on /api/videos,
 * which a machine may serve.
 */
export function videosUrl(query: string): string {
  const favorite = new URLSearchParams(query).get("favorite") === "true";
  return `${favorite ? "/api/me/videos" : "/api/videos"}?${query}`;
}
