// Reads GET /api/tv/cluster and re-reads it every 5 s while the page is
// visible; a hidden tab does not poll, and showing it again reads at once.
// A 403 / 404 (not an admin, or a server without the cluster) stops polling.
import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "../../../lib/api";
import { tvClusterApi, type TvClusterView } from "../../../lib/tvClusterApi";

export const CLUSTER_POLL_MS = 5000;

export interface TvClusterState {
  view: TvClusterView | null;
  error: string | null;
  /** the server has no cluster view for this user */
  unavailable: boolean;
  reload: () => Promise<void>;
}

export function useTvCluster(): TvClusterState {
  const [view, setView] = useState<TvClusterView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const off = useRef(false);
  const alive = useRef(true);

  const reload = useCallback(async () => {
    if (off.current) return;
    try {
      const v = await tvClusterApi.get();
      if (!alive.current) return;
      setView(v);
      setError(null);
    } catch (e) {
      if (!alive.current) return;
      if (e instanceof ApiError && (e.status === 403 || e.status === 404)) {
        off.current = true;
        setUnavailable(true);
        return;
      }
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    let timer: number | null = null;
    const start = () => {
      if (timer === null) timer = window.setInterval(() => void reload(), CLUSTER_POLL_MS);
    };
    const stop = () => {
      if (timer !== null) window.clearInterval(timer);
      timer = null;
    };
    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        void reload();
        start();
      } else {
        stop();
      }
    };
    void reload();
    if (document.visibilityState === "visible") start();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      alive.current = false;
      stop();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [reload]);

  return { view, error, unavailable, reload };
}
