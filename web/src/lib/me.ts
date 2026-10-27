// Epic 45 Phase 2 — the caller's own marks on videos (stories 45.8 to 45.14):
//
//   POST /api/me/watched   {video_ids, watched}   -> {updated}
//   POST /api/me/favorites {video_ids, favorite}  -> {updated}
//   GET  /api/me/videos/{id}                      -> MyVideo (404: not visible)
//   GET/POST /api/me/playlists, GET/PATCH/DELETE /api/me/playlists/{id},
//   POST /api/me/playlists/{id}/items, DELETE …/items/{video_id},
//   PUT /api/me/playlists/{id}/order
//   GET /api/videos/facets?library=               -> Facets
//   GET /api/series/{id}/episodes                 -> the series' lessons, in order
//
// The server takes at most 500 videos a call; a bigger selection is sent in
// chunks. Every reader is defensive: a missing list is an empty one.
import { api } from "./api";
import type { VideoCard } from "./browse";

/** The most videos one call to the marks routes takes. */
export const MARK_CHUNK = 500;

function chunks<T>(xs: T[], size = MARK_CHUNK): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < xs.length; i += size) out.push(xs.slice(i, i + size));
  return out;
}

const list = <T>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);
const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};
const num = (v: unknown, d = 0) => (typeof v === "number" && Number.isFinite(v) ? v : d);
const str = (v: unknown, d = "") => (typeof v === "string" ? v : d);

async function sumChunks(path: string, ids: string[], body: Record<string, unknown>) {
  let updated = 0;
  for (const part of chunks(ids)) {
    const r = obj(await api.post<unknown>(path, { video_ids: part, ...body }));
    updated += num(r.updated, part.length);
  }
  return updated;
}

/** Marks videos watched or not; the number the server changed. */
export function markWatched(ids: string[], watched: boolean): Promise<number> {
  return sumChunks("/api/me/watched", ids, { watched });
}

/** Adds videos to or removes them from the caller's favorites. */
export function setFavorite(ids: string[], favorite: boolean): Promise<number> {
  return sumChunks("/api/me/favorites", ids, { favorite });
}

export interface MyVideo {
  video_id: string;
  position_sec: number;
  completed: boolean;
  favorite: boolean;
  playlists: Array<{ id: string; name: string }>;
  series: { id: string; name: string } | null;
}

export async function fetchMyVideo(id: string): Promise<MyVideo> {
  const r = obj(await api.get<unknown>(`/api/me/videos/${encodeURIComponent(id)}`));
  const s = obj(r.series);
  return {
    video_id: str(r.video_id, id),
    position_sec: num(r.position_sec),
    completed: r.completed === true,
    favorite: r.favorite === true,
    playlists: list<{ id: string; name: string }>(r.playlists).filter(
      (p) => p && typeof p.id === "string"
    ),
    series: typeof s.id === "string" ? { id: s.id, name: str(s.name) } : null,
  };
}

// ---- playlists ----

/** The longest playlist name the server takes. */
export const PLAYLIST_NAME_MAX = 200;

export interface PlaylistSummary {
  id: string;
  name: string;
  description: string;
  videos: number;
  duration_sec: number;
  poster_video_ids: string[];
  created_at?: string;
  updated_at?: string;
}

export interface PlaylistDetail {
  playlist: PlaylistSummary;
  items: VideoCard[];
}

function toSummary(v: unknown): PlaylistSummary {
  const r = obj(v);
  return {
    id: str(r.id),
    name: str(r.name),
    description: str(r.description),
    videos: num(r.videos),
    duration_sec: num(r.duration_sec),
    poster_video_ids: list<string>(r.poster_video_ids)
      .filter((x) => typeof x === "string")
      .slice(0, 4),
    created_at: typeof r.created_at === "string" ? r.created_at : undefined,
    updated_at: typeof r.updated_at === "string" ? r.updated_at : undefined,
  };
}

function toDetail(v: unknown): PlaylistDetail {
  const r = obj(v);
  return {
    playlist: toSummary(r.playlist),
    items: list<VideoCard>(r.items).filter((c) => c && typeof c.id === "string"),
  };
}

const plPath = (id: string) => `/api/me/playlists/${encodeURIComponent(id)}`;

export async function fetchPlaylists(): Promise<PlaylistSummary[]> {
  const r = obj(await api.get<unknown>("/api/me/playlists"));
  return list<unknown>(r.items)
    .map(toSummary)
    .filter((p) => p.id);
}

export async function createPlaylist(
  name: string,
  description = "",
  videoIds: string[] = []
): Promise<PlaylistSummary> {
  // the first 500 go with the new playlist, the rest are added after
  const [first = [], ...rest] = chunks(videoIds);
  const created = toSummary(
    await api.post<unknown>("/api/me/playlists", {
      name,
      ...(description ? { description } : {}),
      ...(first.length ? { video_ids: first } : {}),
    })
  );
  for (const part of rest) await addToPlaylist(created.id, part);
  return created;
}

export async function fetchPlaylist(id: string): Promise<PlaylistDetail> {
  return toDetail(await api.get<unknown>(plPath(id)));
}

export async function updatePlaylist(
  id: string,
  patch: { name?: string; description?: string }
): Promise<PlaylistSummary> {
  return toSummary(await api.patch<unknown>(plPath(id), patch));
}

export async function deletePlaylist(id: string): Promise<void> {
  await api.delete(plPath(id));
}

/** Appends the videos not already in the playlist; how many were added. */
export async function addToPlaylist(id: string, videoIds: string[]): Promise<number> {
  let added = 0;
  for (const part of chunks(videoIds)) {
    const r = obj(await api.post<unknown>(`${plPath(id)}/items`, { video_ids: part }));
    added += num(r.added);
  }
  return added;
}

export async function removeFromPlaylist(id: string, videoId: string): Promise<void> {
  await api.delete(`${plPath(id)}/items/${encodeURIComponent(videoId)}`);
}

export async function reorderPlaylist(id: string, videoIds: string[]): Promise<PlaylistDetail> {
  return toDetail(await api.put<unknown>(`${plPath(id)}/order`, { video_ids: videoIds }));
}

// ---- facets ----

export interface Facets {
  languages: Array<{ code: string; videos: number }>;
  tags: Array<{ id: string; name: string; videos: number }>;
  speakers: Array<{ name: string; videos: number }>;
  letters: Array<{ letter: string; videos: number }>;
  /** Phase 3 (story 45.20): the length, added-date and resolution bands with videos */
  durations: BandFacet[];
  added: BandFacet[];
  resolutions: BandFacet[];
}

export interface BandFacet {
  value: string;
  videos: number;
}

export const EMPTY_FACETS: Facets = {
  languages: [],
  tags: [],
  speakers: [],
  letters: [],
  durations: [],
  added: [],
  resolutions: [],
};

const bands = (v: unknown): BandFacet[] =>
  list<BandFacet>(v).filter(
    (x) => x && typeof x.value === "string" && typeof x.videos === "number"
  );

export async function fetchFacets(libraryId?: string): Promise<Facets> {
  const qs = libraryId ? `?library=${encodeURIComponent(libraryId)}` : "";
  const r = obj(await api.get<unknown>(`/api/videos/facets${qs}`));
  return {
    languages: list<Facets["languages"][number]>(r.languages).filter(
      (x) => x && typeof x.code === "string" && x.code !== ""
    ),
    tags: list<Facets["tags"][number]>(r.tags).filter(
      (x) => x && typeof x.id === "string" && typeof x.name === "string"
    ),
    speakers: list<Facets["speakers"][number]>(r.speakers).filter(
      (x) => x && typeof x.name === "string" && x.name !== ""
    ),
    letters: list<Facets["letters"][number]>(r.letters).filter(
      (x) => x && typeof x.letter === "string" && x.letter !== ""
    ),
    durations: bands(r.durations),
    added: bands(r.added),
    resolutions: bands(r.resolutions),
  };
}

// ---- a series' lessons ----

export interface Lesson {
  video_id: string;
  title: string;
  watched: boolean;
  progress_pct: number;
}

/** A series' lessons in order (its seasons one after the other). */
export async function fetchLessons(seriesId: string): Promise<Lesson[]> {
  const r = obj(await api.get<unknown>(`/api/series/${encodeURIComponent(seriesId)}/episodes`));
  const out: Lesson[] = [];
  for (const s of list<unknown>(r.seasons)) {
    for (const e of list<unknown>(obj(s).episodes)) {
      const ep = obj(e);
      if (typeof ep.video_id !== "string") continue;
      out.push({
        video_id: ep.video_id,
        title: str(ep.title),
        watched: ep.watched === true,
        progress_pct: num(ep.progress_pct),
      });
    }
  }
  return out;
}

export interface SeriesPosition {
  series: { id: string; name: string };
  index: number; // 0-based
  total: number;
  watched: number;
  previous: Lesson | null;
  next: Lesson | null;
}

/** Where a video sits in its series, or null when it is not one of its lessons. */
export async function seriesPosition(
  series: { id: string; name: string },
  videoId: string
): Promise<SeriesPosition | null> {
  const lessons = await fetchLessons(series.id);
  const index = lessons.findIndex((l) => l.video_id === videoId);
  if (index < 0) return null;
  return {
    series,
    index,
    total: lessons.length,
    watched: lessons.filter((l) => l.watched).length,
    previous: lessons[index - 1] ?? null,
    next: lessons[index + 1] ?? null,
  };
}
