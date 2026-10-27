// Epic 45 Phase 3 (story 45.17) — a library's collections:
//
//   GET    /api/collections?library=<id>       -> { items: Collection[] } (404: not visible)
//   GET    /api/collections/{id}               -> Collection
//   GET    /api/collections/{id}/items         -> { items: [{video_id, position, video}] }
//   POST   /api/collections                    {library_id, name, is_smart?, smart_query?}
//   PATCH  /api/collections/{id}               {name?, smart_query?, items?}
//   DELETE /api/collections/{id}
//   POST   /api/collections/{id}/items         {video_ids} -> { added }
//   DELETE /api/collections/{id}/items/{video}
//
// Everyone reads the collections of the libraries they may see; only admins
// write (the server refuses everyone else). A manual collection is a list an
// admin keeps in order; a smart one here is a saved grid filter (smart_query
// {"filters": {...}}), opened as the library's Browse tab with those filters.
import { api } from "./api";
import type { VideoCard } from "./browse";

export const COLLECTION_NAME_MAX = 200;

/** The grid parameters a saved filter may hold (the server's FilterKeys). */
export const SAVED_FILTER_KEYS = [
  "q",
  "language",
  "year",
  "watched",
  "series",
  "tag",
  "speaker",
  "letter",
  "duration",
  "added",
  "resolution",
  "sort",
  "order",
] as const;

export interface Collection {
  id: string;
  library_id: string;
  name: string;
  description?: string | null;
  is_smart: boolean;
  /** a saved filter's grid parameters; null for a manual collection or another kind of smart one */
  filters: Record<string, string> | null;
  /** a manual collection's visible videos */
  videos: number | null;
  poster_video_ids: string[];
}

const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};

function filtersOf(smart: unknown): Record<string, string> | null {
  const f = obj(obj(smart).filters);
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(f)) if (typeof v === "string" && v) out[k] = v;
  return Object.keys(out).length > 0 ? out : null;
}

function readCollection(raw: unknown): Collection | null {
  const c = obj(raw);
  if (typeof c.id !== "string" || typeof c.library_id !== "string") return null;
  return {
    id: c.id,
    library_id: c.library_id,
    name: typeof c.name === "string" ? c.name : "",
    description: typeof c.description === "string" ? c.description : null,
    is_smart: c.is_smart === true,
    filters: c.is_smart === true ? filtersOf(c.smart_query) : null,
    videos: typeof c.videos === "number" ? c.videos : null,
    poster_video_ids: Array.isArray(c.poster_video_ids)
      ? (c.poster_video_ids as unknown[])
          .filter((x): x is string => typeof x === "string")
          .slice(0, 4)
      : [],
  };
}

export async function fetchCollections(libraryId: string): Promise<Collection[]> {
  const r = obj(
    await api.get<unknown>(`/api/collections?library=${encodeURIComponent(libraryId)}`)
  );
  return (Array.isArray(r.items) ? r.items : [])
    .map(readCollection)
    .filter((c): c is Collection => c !== null);
}

export async function fetchCollection(id: string): Promise<Collection> {
  const c = readCollection(await api.get<unknown>(`/api/collections/${encodeURIComponent(id)}`));
  if (!c) throw new Error("malformed collection");
  return c;
}

/** A manual collection's videos, in its order. */
export async function fetchCollectionItems(id: string): Promise<VideoCard[]> {
  const r = obj(await api.get<unknown>(`/api/collections/${encodeURIComponent(id)}/items`));
  const out: VideoCard[] = [];
  for (const raw of Array.isArray(r.items) ? r.items : []) {
    const it = obj(raw);
    const v = obj(it.video);
    if (typeof it.video_id !== "string") continue;
    out.push({
      id: it.video_id,
      title: typeof v.title === "string" ? v.title : null,
      filename: typeof v.filename === "string" ? v.filename : null,
      library_id: typeof v.library_id === "string" ? v.library_id : undefined,
      duration_sec: typeof v.duration_sec === "number" ? v.duration_sec : null,
      state: typeof v.state === "string" ? v.state : undefined,
    });
  }
  return out;
}

export async function createCollection(
  libraryId: string,
  name: string,
  filters?: Record<string, string>
): Promise<Collection> {
  const body: Record<string, unknown> = { library_id: libraryId, name };
  if (filters) {
    body.is_smart = true;
    body.smart_query = { filters };
  }
  const c = readCollection(await api.post<unknown>("/api/collections", body));
  if (!c) throw new Error("malformed collection");
  return c;
}

export async function renameCollection(id: string, name: string): Promise<void> {
  await api.patch(`/api/collections/${encodeURIComponent(id)}`, { name });
}

export async function saveCollectionFilters(
  id: string,
  filters: Record<string, string>
): Promise<void> {
  await api.patch(`/api/collections/${encodeURIComponent(id)}`, { smart_query: { filters } });
}

/** The new order of a manual collection: positions 10, 20, 30 … */
export async function reorderCollection(id: string, videoIds: string[]): Promise<void> {
  await api.patch(`/api/collections/${encodeURIComponent(id)}`, {
    items: videoIds.map((video_id, i) => ({ video_id, position: (i + 1) * 10 })),
  });
}

export async function deleteCollection(id: string): Promise<void> {
  await api.delete(`/api/collections/${encodeURIComponent(id)}`);
}

/** Appends videos at the end (500 a call); how many went in. */
export async function addToCollection(id: string, videoIds: string[]): Promise<number> {
  let added = 0;
  for (let i = 0; i < videoIds.length; i += 500) {
    const r = obj(
      await api.post<unknown>(`/api/collections/${encodeURIComponent(id)}/items`, {
        video_ids: videoIds.slice(i, i + 500),
      })
    );
    added += typeof r.added === "number" ? r.added : 0;
  }
  return added;
}

export async function removeFromCollection(id: string, videoId: string): Promise<void> {
  await api.delete(
    `/api/collections/${encodeURIComponent(id)}/items/${encodeURIComponent(videoId)}`
  );
}

/** The grid parameters of a saved filter, kept to the ones the server takes. */
export function pickFilters(params: URLSearchParams): Record<string, string> {
  const out: Record<string, string> = {};
  for (const k of SAVED_FILTER_KEYS) {
    const v = params.get(k);
    if (v) out[k] = v;
  }
  return out;
}

/** Where a collection opens: a saved filter on its library's Browse tab. */
export function collectionPath(c: Collection): string {
  return `/collections/${encodeURIComponent(c.id)}`;
}

export function browsePathFor(c: Collection): string {
  const qs = new URLSearchParams({ tab: "browse", ...(c.filters ?? {}) });
  return `/library/${encodeURIComponent(c.library_id)}?${qs.toString()}`;
}
