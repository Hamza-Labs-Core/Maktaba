import { useEffect, useState } from "react";

/** How long a page may load before its placeholder shows: most loads are done by then. */
export const PLACEHOLDER_DELAY_MS = 400;

/**
 * True once `on` has stayed true for `ms`; false again as soon as it is not.
 * A loading placeholder waits on it, so a quick load never flashes one.
 */
export function useDelayedFlag(on: boolean, ms: number = PLACEHOLDER_DELAY_MS): boolean {
  const [late, setLate] = useState(false);
  useEffect(() => {
    setLate(false);
    if (!on) return;
    const timer = setTimeout(() => setLate(true), ms);
    return () => clearTimeout(timer);
  }, [on, ms]);
  return on && late;
}
