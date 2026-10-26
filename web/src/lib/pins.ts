// The signed-in user's pinned libraries (Epic 45), in their order:
//
//   GET /api/me/pinned-libraries          -> { items: [{ id, name }] }
//   PUT /api/me/pinned-libraries          { library_ids: [...] } (the order is the array's)
//
// The sidebar's "Pinned" group, the Libraries page and a library's page share
// one store: the nav reads it once per user, and a page that changes the pins
// publishes the new list so the nav follows at once without another read.
import { useEffect, useState } from "react";
import { api } from "./api";
import type { LibraryRef } from "./browse";

export const PINS_PATH = "/api/me/pinned-libraries";
/** The server keeps at most this many pins. */
export const MAX_PINS = 20;

let pins: LibraryRef[] | null = null;
let loadedFor: string | null = null;
let inflight: Promise<LibraryRef[]> | null = null;
const listeners = new Set<(p: LibraryRef[] | null) => void>();

function publish(next: LibraryRef[] | null) {
  pins = next;
  for (const l of listeners) l(next);
}

// { items: [...] } (the API's list convention); a bare array or { libraries } is read too.
function parse(r: unknown): LibraryRef[] {
  const o = r && typeof r === "object" ? (r as Record<string, unknown>) : {};
  const raw = Array.isArray(r) ? r : Array.isArray(o.items) ? o.items : o.libraries;
  return (Array.isArray(raw) ? raw : [])
    .filter((p) => p && typeof p === "object" && typeof (p as LibraryRef).id === "string")
    .map((p) => ({ id: (p as LibraryRef).id, name: String((p as LibraryRef).name ?? "") }));
}

/** Reads the pins for `user` (once; later calls share the answer). */
export function loadPins(user: string): Promise<LibraryRef[]> {
  if (loadedFor === user && pins) return Promise.resolve(pins);
  if (loadedFor === user && inflight) return inflight;
  loadedFor = user;
  const p = api
    .get<unknown>(PINS_PATH)
    .then((r) => {
      const list = parse(r);
      if (loadedFor === user) publish(list);
      return list;
    })
    .catch(() => {
      // an older server, or a moment offline: no pinned group, nothing breaks
      if (loadedFor === user) publish([]);
      return [] as LibraryRef[];
    })
    .finally(() => {
      if (inflight === p) inflight = null;
    });
  inflight = p;
  return p;
}

/**
 * Replaces the pins (the order is the list's). The nav shows the new list at
 * once; when the server refuses, the old list comes back and the error is thrown.
 */
export async function savePins(next: LibraryRef[]): Promise<LibraryRef[]> {
  const before = pins;
  publish(next);
  try {
    const r = await api.put<unknown>(PINS_PATH, { library_ids: next.map((p) => p.id) });
    const answer = parse(r);
    // the server's names win; an empty answer (204) keeps what was sent
    const final = answer.length > 0 || next.length === 0 ? answer : next;
    publish(final);
    return final;
  } catch (e) {
    publish(before);
    throw e;
  }
}

/** Pins `lib` at the end, or unpins it. */
export function togglePin(lib: LibraryRef, current: LibraryRef[]): Promise<LibraryRef[]> {
  const has = current.some((p) => p.id === lib.id);
  return savePins(has ? current.filter((p) => p.id !== lib.id) : [...current, lib]);
}

/** Moves the pin at `index` by `delta` (-1 up, +1 down). */
export function movePin(
  current: LibraryRef[],
  index: number,
  delta: number
): Promise<LibraryRef[]> {
  const to = index + delta;
  if (to < 0 || to >= current.length) return Promise.resolve(current);
  const next = current.slice();
  const [item] = next.splice(index, 1);
  next.splice(to, 0, item!);
  return savePins(next);
}

/** The pins of `user` (null while they load, or with no user). */
export function usePins(user: string | null | undefined): LibraryRef[] | null {
  const [state, setState] = useState<LibraryRef[] | null>(user && loadedFor === user ? pins : null);
  useEffect(() => {
    if (!user) return;
    listeners.add(setState);
    if (loadedFor === user && pins) setState(pins);
    else void loadPins(user);
    return () => {
      listeners.delete(setState);
    };
  }, [user]);
  return user ? state : null;
}

/** Test seam: forget the shared pins. */
export function resetPinsStore() {
  pins = null;
  loadedFor = null;
  inflight = null;
}
