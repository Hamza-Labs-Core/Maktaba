// Epic 45 Phase 4 (story 45.24) — a library's Series and Collections tabs:
//
//   GET /api/libraries/{id}/series?sort=name|newest|recent&offset=
//       -> { items: LibrarySeries[], total, next }
//   GET /api/libraries/{id}/collections?kind=all|topic|filler|standalone|curated&offset=
//       -> { items: LibraryCollection[], counts, total, next }
//   GET /api/libraries/{id}/collections/{topic|clip}/{gid}?offset=
//       -> { collection, items: VideoCard[], total, next }
//
// The collections are the pipeline's automatic groups (topic collections,
// clip groups: filler and stand-alone), the ones the Plex extension writes to
// Plex, and the admins' curated lists and saved filters. A library the user
// may not see answers 404 everywhere.
import { api } from "./api";
import type { VideoCard } from "./browse";

export type SeriesSort = "name" | "newest" | "recent";
export const SERIES_SORTS: SeriesSort[] = ["name", "newest", "recent"];

export interface LibrarySeries {
  id: string;
  name: string;
  poster_path?: string | null;
  poster_video_id?: string | null;
  episodes: number;
  watched: number;
  in_progress: number;
  latest_added_at?: string | null;
  last_watched_at?: string | null;
}

/** The Collections tab's kinds, in their order (the filter's chips). */
export type CollectionFilter = "topic" | "filler" | "standalone" | "curated";
export const COLLECTION_FILTERS: CollectionFilter[] = ["topic", "filler", "standalone", "curated"];

export type CollectionKind = "topic" | "clip" | "curated" | "smart";

export interface LibraryCollection {
  kind: CollectionKind;
  /** a clip group's: filler or standalone */
  subkind?: "filler" | "standalone";
  id: string;
  name: string;
  /** its visible videos; null for a saved filter */
  videos: number | null;
  poster_video_ids: string[];
  /** made by the pipeline (a topic or clip group): read-only */
  automatic: boolean;
}

export interface Paged<T> {
  items: T[];
  total: number;
  /** the next page's offset, null on the last page */
  next: number | null;
}

export interface CollectionsPage extends Paged<LibraryCollection> {
  counts: Record<CollectionFilter | "all", number>;
}

export interface AutoCollection {
  kind: "topic" | "clip";
  subkind?: "filler" | "standalone";
  id: string;
  name: string;
  library: { id: string; name: string };
  videos: number;
}

export interface AutoCollectionPage extends Paged<VideoCard> {
  collection: AutoCollection;
}

const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const num = (v: unknown, d = 0): number => (typeof v === "number" ? v : d);
const str = (v: unknown): string => (typeof v === "string" ? v : "");

function paged<T>(r: Record<string, unknown>, read: (x: unknown) => T | null): Paged<T> {
  const items = arr(r.items)
    .map(read)
    .filter((x): x is T => x !== null);
  return {
    items,
    total: num(r.total, items.length),
    next: typeof r.next === "number" ? r.next : null,
  };
}

const libPath = (libraryId: string) => `/api/libraries/${encodeURIComponent(libraryId)}`;

function readSeries(raw: unknown): LibrarySeries | null {
  const s = obj(raw);
  if (typeof s.id !== "string") return null;
  return {
    id: s.id,
    name: str(s.name),
    poster_path: typeof s.poster_path === "string" ? s.poster_path : null,
    poster_video_id: typeof s.poster_video_id === "string" ? s.poster_video_id : null,
    episodes: num(s.episodes),
    watched: num(s.watched),
    in_progress: num(s.in_progress),
    latest_added_at: typeof s.latest_added_at === "string" ? s.latest_added_at : null,
    last_watched_at: typeof s.last_watched_at === "string" ? s.last_watched_at : null,
  };
}

export async function fetchLibrarySeries(
  libraryId: string,
  sort: SeriesSort,
  offset = 0
): Promise<Paged<LibrarySeries>> {
  const qs = new URLSearchParams({ sort });
  if (offset > 0) qs.set("offset", String(offset));
  return paged(obj(await api.get<unknown>(`${libPath(libraryId)}/series?${qs}`)), readSeries);
}

const KINDS: CollectionKind[] = ["topic", "clip", "curated", "smart"];

function readCollection(raw: unknown): LibraryCollection | null {
  const c = obj(raw);
  if (typeof c.id !== "string" || !KINDS.includes(c.kind as CollectionKind)) return null;
  const kind = c.kind as CollectionKind;
  return {
    kind,
    subkind: c.subkind === "filler" || c.subkind === "standalone" ? c.subkind : undefined,
    id: c.id,
    name: str(c.name),
    videos: typeof c.videos === "number" ? c.videos : null,
    poster_video_ids: arr(c.poster_video_ids)
      .filter((x): x is string => typeof x === "string")
      .slice(0, 4),
    automatic: kind === "topic" || kind === "clip",
  };
}

/** The chip a collection belongs to. */
export function filterOf(c: Pick<LibraryCollection, "kind" | "subkind">): CollectionFilter {
  if (c.kind === "clip") return c.subkind === "standalone" ? "standalone" : "filler";
  if (c.kind === "smart") return "curated";
  return c.kind;
}

export async function fetchLibraryCollections(
  libraryId: string,
  kind: CollectionFilter | "all" = "all",
  offset = 0
): Promise<CollectionsPage> {
  const qs = new URLSearchParams();
  if (kind !== "all") qs.set("kind", kind);
  if (offset > 0) qs.set("offset", String(offset));
  const q = qs.toString();
  const r = obj(await api.get<unknown>(`${libPath(libraryId)}/collections${q ? `?${q}` : ""}`));
  const c = obj(r.counts);
  return {
    ...paged(r, readCollection),
    counts: {
      all: num(c.all),
      topic: num(c.topic),
      filler: num(c.filler),
      standalone: num(c.standalone),
      curated: num(c.curated),
    },
  };
}

function readCard(raw: unknown): VideoCard | null {
  const v = obj(raw);
  if (typeof v.id !== "string") return null;
  return {
    id: v.id,
    title: typeof v.title === "string" ? v.title : null,
    filename: typeof v.filename === "string" ? v.filename : null,
    library_id: typeof v.library_id === "string" ? v.library_id : undefined,
    duration_sec: typeof v.duration_sec === "number" ? v.duration_sec : null,
    state: typeof v.state === "string" ? v.state : undefined,
    position_sec: typeof v.position_sec === "number" ? v.position_sec : null,
    completed: typeof v.completed === "boolean" ? v.completed : null,
  };
}

export async function fetchAutoCollection(
  libraryId: string,
  kind: string,
  id: string,
  offset = 0
): Promise<AutoCollectionPage> {
  const q = offset > 0 ? `?offset=${offset}` : "";
  const r = obj(
    await api.get<unknown>(
      `${libPath(libraryId)}/collections/${encodeURIComponent(kind)}/${encodeURIComponent(id)}${q}`
    )
  );
  const c = obj(r.collection);
  const lib = obj(c.library);
  return {
    ...paged(r, readCard),
    collection: {
      kind: c.kind === "clip" ? "clip" : "topic",
      subkind: c.subkind === "filler" || c.subkind === "standalone" ? c.subkind : undefined,
      id: str(c.id) || id,
      name: str(c.name),
      library: { id: str(lib.id) || libraryId, name: str(lib.name) },
      videos: num(c.videos),
    },
  };
}

/** Where a collection of the tab opens. */
export function libraryCollectionPath(libraryId: string, c: LibraryCollection): string {
  if (c.automatic)
    return `/library/${encodeURIComponent(libraryId)}/collections/${c.kind}/${encodeURIComponent(c.id)}`;
  return `/collections/${encodeURIComponent(c.id)}`;
}

/** A library's tab: `/library/:id?tab=…` (Overview has none). */
export function libraryTabPath(
  libraryId: string,
  tab: "series" | "collections" | "browse"
): string {
  return `/library/${encodeURIComponent(libraryId)}?tab=${tab}`;
}
