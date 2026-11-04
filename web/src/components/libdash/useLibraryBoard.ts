// The reads behind the library dashboard and a library's queue section: every
// library's queue row (GET /api/queues/libraries), the Epic 47 summaries
// (GET /api/queues/offline-summary), two weeks of finished jobs per library
// (GET /api/queues/libraries/activity) and the stages (GET /api/queues). All are
// the admins' (the API refuses anyone else); a read that fails leaves its part
// empty, so the rest still shows. The library rows, which the page waits on,
// are never waited on forever: past LIBRARIES_SLOW_MS the page says it is slow
// (with Try again), and past LIBRARIES_TIMEOUT_MS the read counts as failed.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { queueApi } from "../../lib/queueApi";
import { useReadOrder } from "../../lib/readOrder";
import type {
  LibraryActivityResponse,
  LibraryQueueRow,
  OfflineSummary,
  StageInfo,
} from "../../lib/queueApi";
import { safeLibraries, safeStage } from "../../pages/queue/safeData";

export interface Activity {
  days: string[];
  done: number[];
  failed: number[];
}

/** How long the library rows may take before the page says the read is slow. */
export const LIBRARIES_SLOW_MS = 8_000;
/** How long the library rows may take before the read counts as failed. */
export const LIBRARIES_TIMEOUT_MS = 30_000;

/** A read that has not answered after ms fails (its late answer is ignored). */
export function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("timed out")), ms);
    p.then(
      (v) => {
        clearTimeout(timer);
        resolve(v);
      },
      (e: unknown) => {
        clearTimeout(timer);
        reject(e instanceof Error ? e : new Error(String(e)));
      }
    );
  });
}

export interface LibraryBoard {
  rows: LibraryQueueRow[] | null;
  offline: Record<string, OfflineSummary> | undefined;
  activity: Map<string, Activity> | null;
  stages: StageInfo[];
  /** the last read of the library rows failed or timed out */
  error: boolean;
  /** a read of the library rows is under way and has taken over LIBRARIES_SLOW_MS */
  slow: boolean;
  reload: () => void;
  /**
   * Reads again after a change made elsewhere (a source removed, work queued):
   * the reads under way began before it and are dropped.
   */
  reloadAfterChange: () => void;
}

export function activityById(r: LibraryActivityResponse | null | undefined): Map<string, Activity> {
  const days = Array.isArray(r?.days) ? r.days : [];
  const zero = days.map(() => 0);
  const m = new Map<string, Activity>();
  for (const it of Array.isArray(r?.items) ? r.items : []) {
    m.set(it.library_id, {
      days,
      done: days.map((_, i) => it.done?.[i] ?? 0),
      failed: days.map((_, i) => it.failed?.[i] ?? 0),
    });
  }
  // a library with nothing finished in the window still gets its (flat) days
  m.set("", { days, done: zero, failed: zero });
  return m;
}

export function activityOf(m: Map<string, Activity> | null, id: string): Activity | undefined {
  if (!m) return undefined;
  return m.get(id) ?? m.get("");
}

export function useLibraryBoard(days = 14): LibraryBoard {
  const [rows, setRows] = useState<LibraryQueueRow[] | null>(null);
  const [offline, setOffline] = useState<Record<string, OfflineSummary> | undefined>(undefined);
  const [activity, setActivity] = useState<Map<string, Activity> | null>(null);
  const [stages, setStages] = useState<StageInfo[]>([]);
  const [error, setError] = useState(false);
  const [slow, setSlow] = useState(false);
  const alive = useRef(true);
  // One ticket per round of the four reads: an answer from an older round (it
  // began before a change, or a newer round's answer is already shown) is dropped.
  const order = useReadOrder();
  // the newest round, for its slow notice: an older round never sets or clears it
  const latest = useRef(0);
  const slowTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const reload = useCallback(() => {
    const ticket = order.begin();
    const fresh = () => alive.current && order.fresh(ticket);
    latest.current = ticket;
    const newest = () => alive.current && latest.current === ticket;
    clearTimeout(slowTimer.current);
    setSlow(false);
    slowTimer.current = setTimeout(() => newest() && setSlow(true), LIBRARIES_SLOW_MS);
    withTimeout(queueApi.libraries(), LIBRARIES_TIMEOUT_MS)
      .then((r) => {
        if (!fresh()) return;
        setRows(safeLibraries(r?.items));
        setError(false);
      })
      .catch(() => fresh() && setError(true))
      .finally(() => {
        if (latest.current !== ticket) return;
        clearTimeout(slowTimer.current);
        if (alive.current) setSlow(false);
      });
    queueApi
      .offlineSummaries()
      .then((r) => {
        if (Array.isArray(r?.items) && fresh())
          setOffline(Object.fromEntries(r.items.map((s) => [s.library_id, s])));
      })
      .catch(() => {});
    queueApi
      .libraryActivity(days)
      .then((r) => fresh() && setActivity(activityById(r)))
      .catch(() => {});
    queueApi
      .snapshot()
      .then((s) => {
        if (Array.isArray(s?.stages) && fresh()) setStages(s.stages.map(safeStage));
      })
      .catch(() => {});
  }, [days, order]);
  const reloadAfterChange = useCallback(() => {
    order.changed();
    reload();
  }, [order, reload]);

  useEffect(() => {
    alive.current = true;
    reload();
    return () => {
      alive.current = false;
      clearTimeout(slowTimer.current);
    };
  }, [reload]);

  return useMemo(
    () => ({ rows, offline, activity, stages, error, slow, reload, reloadAfterChange }),
    [rows, offline, activity, stages, error, slow, reload, reloadAfterChange]
  );
}
