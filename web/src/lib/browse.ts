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
  continue_watching: VideoCard[];
  recently_added: VideoCard[];
  recently_watched: VideoCard[];
  pinned: PinnedRows[];
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
    continue_watching: VideoCard[];
    recently_added: VideoCard[];
    recently_watched: VideoCard[];
    most_watched: VideoCard[];
    series: HubSeries[];
    years: HubYear[];
  };
}

const list = <T>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);
const cards = (v: unknown) =>
  list<VideoCard>(v).filter((c) => c && typeof c === "object" && typeof c.id === "string");
const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};

export async function fetchHome(): Promise<HomeResponse> {
  const r = obj(await api.get<unknown>("/api/home"));
  return {
    continue_watching: cards(r.continue_watching),
    recently_added: cards(r.recently_added),
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
      continue_watching: cards(rows.continue_watching),
      recently_added: cards(rows.recently_added),
      recently_watched: cards(rows.recently_watched),
      most_watched: cards(rows.most_watched),
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
