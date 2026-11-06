// The play queue (Epic 45 story 45.13, on the server since Epic 56 story 56.13):
// Play all, Shuffle, Play from here and a playlist make a queue, and the
// player's Up next plays it one video after the other.
//
// The queue lives on the server (POST /api/me/play-queues, story 56.01): it
// can hold a whole library, Shuffle is a seeded order of all of it (not one
// random page), and a queue started here can be continued on the TV or the
// phone. This tab keeps only the queue's id and the window of items around the
// current one (sessionStorage); a value from before (a list of ids) is read
// once, ignored and replaced on the next Play.
//
// A grid with no library (every library's videos) has no server source: its
// Play all keeps the old in-tab queue of the grid's pages.
import { api, ApiError } from "./api";
import { videosUrl } from "./browse";

const KEY = "mkt:playQueue";

/** How many ids an in-tab queue reads at a time from GET /api/videos. */
export const QUEUE_PAGE = 200;

/** The keys of GET /api/videos a library source carries (the grid's filters and sort). */
export const SOURCE_FILTER_KEYS = [
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

export type QueueSourceKind = "library" | "series" | "season" | "collection" | "playlist" | "video";

/** Where a server queue's videos come from. */
export interface QueueSourceRef {
  kind: QueueSourceKind;
  id: string;
  filters?: Record<string, string>;
}

/** One item of a server queue's window. */
export interface QueueItem {
  pos: number;
  origin: "source" | "next" | "added";
  id: string;
  title?: string | null;
  filename?: string;
  library_id?: string;
  duration_sec?: number | null;
  poster?: string;
  position_sec?: number;
  completed?: boolean;
  series_id?: string;
  series_name?: string;
}

/** A server queue as GET /api/me/play-queues/{id} answers it. */
export interface ServerQueue {
  id: string;
  source: QueueSourceRef;
  shuffle: boolean;
  current_pos: number;
  version: number;
  device_id?: string;
  source_done: boolean;
  total_estimate: number;
  used_at?: string;
  items: QueueItem[];
}

/** The in-tab queue of a grid with no library. */
export interface QueueSource {
  /** GET /api/videos's query, without cursor and limit */
  query: string;
  cursor: string | null;
}

export interface PlayQueue {
  ids: string[];
  index: number;
  source?: QueueSource | null;
}

type Stored = { server: ServerQueue } | { local: PlayQueue };

function readStored(): Stored | null {
  try {
    const raw = sessionStorage.getItem(KEY);
    if (!raw) return null;
    const v = JSON.parse(raw) as Partial<{ server: ServerQueue; local: PlayQueue }>;
    if (v?.server && typeof v.server.id === "string" && Array.isArray(v.server.items)) {
      return { server: v.server };
    }
    const l = v?.local;
    if (l && Array.isArray(l.ids) && typeof l.index === "number") return { local: l };
    return null; // a queue from before Epic 56, or anything else: ignored
  } catch {
    return null;
  }
}

function write(v: Stored | null) {
  try {
    if (v) sessionStorage.setItem(KEY, JSON.stringify(v));
    else sessionStorage.removeItem(KEY);
  } catch {
    /* storage blocked: the queue lasts only as long as this page */
  }
}

/** The in-tab queue (a grid with no library), or null. */
export function readQueue(): PlayQueue | null {
  const s = readStored();
  return s && "local" in s ? s.local : null;
}

/** The server queue this tab plays, or null. */
export function readServerQueue(): ServerQueue | null {
  const s = readStored();
  return s && "server" in s ? s.server : null;
}

/** Keeps q as the tab's queue (a window the server answered). */
export function keepServerQueue(q: ServerQueue) {
  write({ server: q });
}

export function clearQueue() {
  write(null);
}

/** The current item of a server queue (null when its window does not hold it). */
export function currentItem(q: ServerQueue): QueueItem | null {
  return q.items.find((it) => it.pos === q.current_pos) ?? null;
}

/** This browser's device id for the queue ("web" and a random tag kept in localStorage). */
function deviceId(): string {
  try {
    let id = localStorage.getItem("mkt:deviceId");
    if (!id) {
      id = `web-${Math.random().toString(36).slice(2, 10)}`;
      localStorage.setItem("mkt:deviceId", id);
    }
    return id;
  } catch {
    return "web";
  }
}

/**
 * Makes a server queue from a source and keeps it; the id of the video to play
 * first, or null when the source has nothing to play (422 empty_source).
 */
export async function playSource(
  source: QueueSourceRef,
  opts: { shuffle?: boolean; startVideoId?: string } = {}
): Promise<string | null> {
  try {
    const q = await api.post<ServerQueue>("/api/me/play-queues", {
      source,
      shuffle: !!opts.shuffle,
      ...(opts.startVideoId ? { start_video_id: opts.startVideoId } : {}),
      device_id: deviceId(),
    });
    keepServerQueue(q);
    return currentItem(q)?.id ?? q.items[0]?.id ?? null;
  } catch (e) {
    if (e instanceof ApiError && e.status === 422 && e.problem.title === "empty_source") {
      clearQueue();
      return null;
    }
    throw e;
  }
}

/** Puts ids in an in-tab queue, the one at `index` playing first; that id (or null). */
export function startQueue(
  ids: string[],
  index = 0,
  source: QueueSource | null = null
): string | null {
  const clean = ids.filter((x) => typeof x === "string" && x);
  if (clean.length === 0) {
    clearQueue();
    return null;
  }
  const at = Math.min(Math.max(0, index), clean.length - 1);
  write({ local: { ids: clean, index: at, source } });
  return clean[at] ?? null;
}

/** A random order of ids (Fisher–Yates). */
export function shuffled<T>(xs: T[], rand: () => number = Math.random): T[] {
  const out = xs.slice();
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rand() * (i + 1));
    [out[i], out[j]] = [out[j]!, out[i]!];
  }
  return out;
}

interface VideosPage {
  items?: Array<{ id: string }> | null;
  next?: string | null;
}

/** Reads a page of ids of GET /api/videos for `query` (no cursor or limit in it). */
async function readPage(query: string, cursor: string | null) {
  const qs = new URLSearchParams(query);
  qs.set("limit", String(QUEUE_PAGE));
  if (cursor) qs.set("cursor", cursor);
  else qs.delete("cursor");
  const r = await api.get<VideosPage>(videosUrl(qs.toString()));
  const ids = (Array.isArray(r?.items) ? r.items : [])
    .map((v) => v?.id)
    .filter((x): x is string => typeof x === "string");
  return { ids, next: typeof r?.next === "string" && r.next ? r.next : null };
}

/** A grid's query as a library source: its library and its filters and sort. */
export function sourceOfQuery(query: string): QueueSourceRef | null {
  const qs = new URLSearchParams(query);
  const lib = qs.get("library");
  if (!lib) return null;
  const filters: Record<string, string> = {};
  for (const k of SOURCE_FILTER_KEYS) {
    const v = qs.get(k);
    if (v) filters[k] = v;
  }
  if (filters.sort === "random") delete filters.sort;
  return { kind: "library", id: lib, filters };
}

/**
 * Play all over a grid's query: a library's grid makes one server queue (Shuffle
 * is a shuffle of the whole view); every library's grid an in-tab queue of its
 * pages (Shuffle one random page). The first id to play, or null when the query
 * lists nothing.
 */
export async function startQueueFromQuery(query: string, shuffle = false): Promise<string | null> {
  const src = sourceOfQuery(query);
  if (src) return playSource(src, { shuffle });
  const qs = new URLSearchParams(query);
  qs.delete("cursor");
  qs.delete("limit");
  if (shuffle) {
    qs.set("sort", "random");
    qs.delete("order");
  }
  const first = await readPage(qs.toString(), null);
  return startQueue(
    first.ids,
    0,
    shuffle || !first.next ? null : { query: qs.toString(), cursor: first.next }
  );
}

/** Whether the queue is playing videoId (it is the queue's current one). */
export function queueHas(videoId: string): boolean {
  const s = readStored();
  if (!s) return false;
  if ("server" in s) return currentItem(s.server)?.id === videoId;
  return s.local.ids[s.local.index] === videoId;
}

/** The window item holding videoId, the current one first. */
function itemOf(q: ServerQueue, videoId: string): QueueItem | undefined {
  const cur = currentItem(q);
  if (cur?.id === videoId) return cur;
  return q.items.find((it) => it.id === videoId);
}

async function readWindow(q: ServerQueue, around: number, before = 10, after = 30) {
  const w = await api.get<ServerQueue>(
    `/api/me/play-queues/${encodeURIComponent(q.id)}?around=${around}&before=${before}&after=${after}`
  );
  keepServerQueue(w);
  return w;
}

/**
 * The id after videoId in the queue (reading more of the queue when the window
 * runs out), or null: the queue ends there or is not playing videoId.
 */
export async function queueNext(videoId: string): Promise<string | null> {
  const s = readStored();
  if (!s) return null;
  if ("server" in s) {
    const q = s.server;
    const it = itemOf(q, videoId);
    if (!it) return null;
    const after = q.items.filter((x) => x.pos > it.pos).sort((a, b) => a.pos - b.pos)[0];
    if (after) return after.id;
    try {
      const w = await readWindow(q, it.pos, 0, 30);
      return w.items.filter((x) => x.pos > it.pos).sort((a, b) => a.pos - b.pos)[0]?.id ?? null;
    } catch {
      return null;
    }
  }
  const q = s.local;
  let at = q.ids[q.index] === videoId ? q.index : q.ids.indexOf(videoId);
  if (at < 0) return null;
  if (at + 1 < q.ids.length) return q.ids[at + 1] ?? null;
  if (!q.source?.cursor) return null;
  try {
    const page = await readPage(q.source.query, q.source.cursor);
    const seen = new Set(q.ids);
    const more = page.ids.filter((id) => !seen.has(id));
    const nq: PlayQueue = {
      ids: [...q.ids, ...more],
      index: at,
      source: page.next ? { query: q.source.query, cursor: page.next } : null,
    };
    write({ local: nq });
    at = nq.ids.indexOf(videoId);
    return nq.ids[at + 1] ?? null;
  } catch {
    return null;
  }
}

/** The event a stale move sends (another device moved the queue): the app says so. */
export const QUEUE_STALE_EVENT = "mkt:queue-stale";

/** Moves the queue on to videoId (the one Up next starts). */
export async function advanceTo(videoId: string): Promise<void> {
  const s = readStored();
  if (!s) return;
  if ("local" in s) {
    const at = s.local.ids.indexOf(videoId);
    if (at >= 0) write({ local: { ...s.local, index: at } });
    return;
  }
  const q = s.server;
  const it = itemOf(q, videoId);
  if (!it) return;
  try {
    const moved = await api.put<ServerQueue>(
      `/api/me/play-queues/${encodeURIComponent(q.id)}/current`,
      { pos: it.pos, version: q.version }
    );
    keepServerQueue(moved);
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      // another device moved it: show the latest and say so
      const latest = (e.problem as unknown as { queue?: ServerQueue }).queue;
      if (latest && Array.isArray(latest.items)) keepServerQueue(latest);
      else await readWindow(q, q.current_pos).catch(() => undefined);
      try {
        window.dispatchEvent(new CustomEvent(QUEUE_STALE_EVENT));
      } catch {
        /* no window (tests) */
      }
      return;
    }
    if (e instanceof ApiError && e.status === 404) clearQueue();
  }
}

/** Play next (where next) or Add to queue (where end); starts a queue when none plays. */
export async function addToQueue(ids: string[], where: "next" | "end"): Promise<boolean> {
  const clean = ids.filter(Boolean);
  if (clean.length === 0) return false;
  let q = readServerQueue();
  if (!q) {
    const first = await playSource({ kind: "video", id: clean[0]! });
    if (!first) return false;
    q = readServerQueue();
    clean.shift();
    if (!q || clean.length === 0) return true;
  }
  const out = await api.post<ServerQueue>(`/api/me/play-queues/${encodeURIComponent(q.id)}/items`, {
    video_ids: clean,
    where,
  });
  keepServerQueue(out);
  return true;
}

/** Shuffle again: a new order after the current video. */
export async function reshuffle(): Promise<void> {
  const q = readServerQueue();
  if (!q?.shuffle) return;
  keepServerQueue(await api.post<ServerQueue>(`/api/me/play-queues/${encodeURIComponent(q.id)}/reshuffle`));
}

/** The caller's most recently used queue, from any device (null when none). */
export async function fetchCurrentQueue(): Promise<ServerQueue | null> {
  const q = await api.get<ServerQueue | undefined | null>("/api/me/play-queues/current");
  return q && typeof q.id === "string" && Array.isArray(q.items) ? q : null;
}

/** Continue a queue (one from another device too): keeps it; the video to play. */
export function resumeQueue(q: ServerQueue): string | null {
  keepServerQueue(q);
  return currentItem(q)?.id ?? null;
}

/** Where the player of a video is. */
export const watchPath = (id: string) => `/videos/${encodeURIComponent(id)}/watch`;
