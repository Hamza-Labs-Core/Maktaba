// Story 58.09 — the saved homes. The last home used opens at once at the next start; the
// home picker lists the others (Account → Switch home on /tv/ opens it). Kept in the
// launcher's own storage: an id, a name and how it was reached, never a credential (the
// sign-in lives in the home's origin, inside the iframe).

const KEY = "maktaba.launcher.homes";
const MAX = 8;

export interface SavedHome {
  id: string;
  name: string;
  base: string;
  plain: boolean;
  /** Where it was found on the LAN (the plain-HTTP fallback), when it was. */
  lan?: string;
  lastUsed: number;
}

interface StorageLike {
  getItem(k: string): string | null;
  setItem(k: string, v: string): void;
}

function store(): StorageLike | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function valid(h: unknown): h is SavedHome {
  if (!h || typeof h !== "object") return false;
  const s = h as Partial<SavedHome>;
  return (
    typeof s.id === "string" &&
    typeof s.name === "string" &&
    typeof s.base === "string" &&
    /^https?:\/\//.test(s.base) &&
    typeof s.lastUsed === "number"
  );
}

/** The saved homes, the last used first. */
export function savedHomes(st: StorageLike | null = store()): SavedHome[] {
  if (!st) return [];
  try {
    const raw: unknown = JSON.parse(st.getItem(KEY) || "[]");
    if (!Array.isArray(raw)) return [];
    return raw.filter(valid).sort((a, b) => b.lastUsed - a.lastUsed);
  } catch {
    return [];
  }
}

/** Saves (or refreshes) a home as the last used. */
export function rememberHome(
  h: Omit<SavedHome, "lastUsed">,
  now = Date.now(),
  st: StorageLike | null = store()
): void {
  if (!st) return;
  const rest = savedHomes(st).filter((x) => x.id !== h.id);
  const next = [{ ...h, lastUsed: now }, ...rest].slice(0, MAX);
  try {
    st.setItem(KEY, JSON.stringify(next));
  } catch {
    // storage full or refused: the home is not kept
  }
}

export function forgetHome(id: string, st: StorageLike | null = store()): void {
  if (!st) return;
  try {
    st.setItem(KEY, JSON.stringify(savedHomes(st).filter((x) => x.id !== id)));
  } catch {
    // refused: nothing to undo
  }
}
