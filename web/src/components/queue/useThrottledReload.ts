// Calls `reload` when `tick` changes, but at most once per `minMs` (a
// trailing call is scheduled so the last change is never dropped). Used
// to refresh REST-backed panels from the job stream without polling.
import { useEffect, useRef } from "react";

export function useThrottledReload(reload: () => void, tick: number, minMs = 10_000) {
  const last = useRef(Date.now());
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const fn = useRef(reload);
  fn.current = reload;
  const first = useRef(true);

  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const wait = last.current + minMs - Date.now();
    if (wait <= 0) {
      last.current = Date.now();
      fn.current();
      return;
    }
    if (timer.current) return;
    timer.current = setTimeout(() => {
      timer.current = null;
      last.current = Date.now();
      fn.current();
    }, wait);
  }, [tick, minMs]);

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    []
  );
}
