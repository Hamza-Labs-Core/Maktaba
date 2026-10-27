// Epic 45 Phase 2 (story 45.13) — the play queue: Play all, Shuffle, Play from
// here and a playlist put a list of videos in it, and the player's Up next
// plays them one after the other. It lives in this browser tab
// (sessionStorage): another tab, or a new visit, starts with none.
//
// A queue made from a big grid holds the first page of ids and the grid's
// query (`source`): when the player reaches the end of what is loaded, the next
// page is read with the cursor GET /api/videos handed back.
import { api } from "./api";
import { videosUrl } from "./browse";

const KEY = "mkt:playQueue";

/** How many ids Play all reads at a time from GET /api/videos. */
export const QUEUE_PAGE = 200;

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

export function readQueue(): PlayQueue | null {
  try {
    const raw = sessionStorage.getItem(KEY);
    if (!raw) return null;
    const q = JSON.parse(raw) as PlayQueue;
    if (!q || !Array.isArray(q.ids) || typeof q.index !== "number") return null;
    return q;
  } catch {
    return null;
  }
}

function write(q: PlayQueue | null) {
  try {
    if (q) sessionStorage.setItem(KEY, JSON.stringify(q));
    else sessionStorage.removeItem(KEY);
  } catch {
    /* storage blocked: the queue lasts only as long as this page */
  }
}

export function clearQueue() {
  write(null);
}

/** Puts ids in the queue, the one at `index` playing first; that id (or null). */
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
  write({ ids: clean, index: at, source });
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

/**
 * Play all over a grid's query: the first page of ids, the rest on demand.
 * Shuffle asks the server for a random page (sort=random, one page, no more).
 * The first id to play, or null when the query lists nothing.
 */
export async function startQueueFromQuery(query: string, shuffle = false): Promise<string | null> {
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
  const q = readQueue();
  return !!q && q.ids[q.index] === videoId;
}

/**
 * The id after videoId in the queue (reading the source's next page when the
 * loaded ids run out), or null: the queue ends there or is not playing videoId.
 */
export async function queueNext(videoId: string): Promise<string | null> {
  const q = readQueue();
  if (!q) return null;
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
    write(nq);
    at = nq.ids.indexOf(videoId);
    return nq.ids[at + 1] ?? null;
  } catch {
    return null;
  }
}

/** Moves the queue on to videoId (the one Up next starts). */
export function advanceTo(videoId: string) {
  const q = readQueue();
  if (!q) return;
  const at = q.ids.indexOf(videoId);
  if (at >= 0) write({ ...q, index: at });
}

/** Where the player of a video is. */
export const watchPath = (id: string) => `/videos/${encodeURIComponent(id)}/watch`;
