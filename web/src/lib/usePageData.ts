// A page's read (GET /api/home, /api/libraries/summary, ...), with its last
// answer kept for the signed-in user (lib/pageCache): coming back to a page
// shows that answer at once and reads it again quietly, never the loading
// placeholder. `loading` is only the first read, with nothing to show yet.
import { useCallback, useEffect, useRef, useState, type DependencyList } from "react";
import { useOptionalAuth } from "./auth";
import { cachedAnswer, keepAnswer } from "./pageCache";

export interface PageData<T> {
  data: T | null;
  /** the last read failed (the page may still show `data` from before) */
  err: unknown;
  /** nothing to show yet: the first read is under way */
  loading: boolean;
  /** read again (the page stays as it is meanwhile) */
  reload: () => void;
  /** a change the page made itself (a rename, a removed item), kept too */
  setData: (next: T | ((prev: T | null) => T | null)) => void;
}

interface Held<T> {
  id: string;
  data: T | null;
  err: unknown;
}

/**
 * `key` names the page and what it shows (e.g. `hub:<library id>`), null when
 * there is nothing to read; `load` reads it. The read runs again when the
 * key, the user or `deps` change.
 */
export function usePageData<T>(
  key: string | null,
  load: () => Promise<T>,
  deps: DependencyList = []
): PageData<T> {
  const user = useOptionalAuth()?.user?.id ?? null;
  const id = `${user ?? ""}\n${key ?? ""}`;
  const fresh = (): Held<T> => ({
    id,
    data: key == null ? null : (cachedAnswer<T>(user, key) ?? null),
    err: null,
  });
  const [held, setHeld] = useState<Held<T>>(fresh);
  const [reloads, setReloads] = useState(0);
  // bumped by a change the page makes: a read that started before it is older
  // than the change and must not overwrite it
  const gen = useRef(0);

  // another page or user: what this one kept (or nothing), never the last one's
  let now = held;
  if (held.id !== id) {
    now = fresh();
    setHeld(now);
  }

  useEffect(() => {
    if (key == null) return;
    let cancelled = false;
    const mine = gen.current;
    setHeld((h) => (h.id === id && h.err != null ? { ...h, err: null } : h));
    load()
      .then((data) => {
        if (cancelled || mine !== gen.current) return;
        keepAnswer(user, key, data);
        setHeld({ id, data, err: null });
      })
      .catch((err: unknown) => {
        if (!cancelled && mine === gen.current) setHeld((h) => (h.id === id ? { ...h, err } : h));
      });
    return () => {
      cancelled = true;
    };
    // `load` is the page's own closure: `key` and `deps` say when it changes
  }, [id, reloads, ...deps]);

  const reload = useCallback(() => setReloads((x) => x + 1), []);
  const setData = useCallback(
    (next: T | ((prev: T | null) => T | null)) =>
      setHeld((h) => {
        // the page moved on (another key or user): this change was the last one's
        if (h.id !== id) return h;
        gen.current += 1;
        const data =
          typeof next === "function" ? (next as (prev: T | null) => T | null)(h.data) : next;
        if (data != null && key != null) keepAnswer(user, key, data);
        return { ...h, data };
      }),
    [id, user, key]
  );

  return {
    data: now.data,
    err: now.err,
    loading: key != null && now.data == null && now.err == null,
    reload,
    setData,
  };
}
