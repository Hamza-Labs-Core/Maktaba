// Runs a mutating API call, tracks busy/error state for the caller.
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "../../lib/api";

export function useAction() {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const run = useCallback(async <T>(key: string, fn: () => Promise<T>): Promise<T | undefined> => {
    setBusy(key);
    setError(null);
    try {
      return await fn();
    } catch (e) {
      if (mounted.current) {
        setError(e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e));
      }
      return undefined;
    } finally {
      if (mounted.current) setBusy(null);
    }
  }, []);
  const clear = useCallback(() => setError(null), []);
  return { busy, error, run, clear };
}
