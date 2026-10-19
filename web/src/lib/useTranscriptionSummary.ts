// Live transcription report.
//
// GET /api/transcription/summary once, then rides the existing 2 s
// `queues.snapshot` stream (/ws/queues): whenever the counters of the
// transcribe / subtitle_gen / index queues move (a job finished, failed,
// started or was added) the summary is re-fetched, at most once per
// RELOAD_MIN_MS. If the stream is silent for SNAPSHOT_SILENCE_MS (proxy
// without SSE, server restart) we poll every SNAPSHOT_POLL_MS until a
// snapshot arrives again.
import { useCallback, useEffect, useRef, useState } from "react";
import type { QueuesSnapshot } from "./queueApi";
import {
  TRANSCRIPTION_STAGES,
  transcriptionApi,
  type TranscriptionSummary,
} from "./transcriptionApi";
import { SNAPSHOT_POLL_MS, SNAPSHOT_SILENCE_MS } from "./useQueueStream";
import { subscribe, type ServerEvent } from "./ws";

export const RELOAD_MIN_MS = 5_000;

export interface TranscriptionState {
  summary: TranscriptionSummary | null;
  error: boolean;
  /** true while the stream is silent and the page polls instead. */
  polling: boolean;
  reload: () => void;
}

/** A fingerprint of the transcription queues in a snapshot. */
export function stagesKey(s: QueuesSnapshot | undefined | null): string {
  if (!s?.queues) return "";
  const want = new Set<string>(TRANSCRIPTION_STAGES);
  return s.queues
    .filter((q) => want.has(q.stage))
    .map(
      (q) =>
        `${q.stage}:${q.counts?.pending}:${q.counts?.running}:${q.counts?.done_24h}:${q.counts?.failed_open}`
    )
    .sort()
    .join("|");
}

export function useTranscriptionSummary(): TranscriptionState {
  const [summary, setSummary] = useState<TranscriptionSummary | null>(null);
  const [error, setError] = useState(false);
  const [polling, setPolling] = useState(false);
  const alive = useRef(true);
  const lastFetch = useRef(0);
  const pendingTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const fetchNow = useCallback(() => {
    lastFetch.current = Date.now();
    return transcriptionApi
      .summary()
      .then((s) => {
        if (!alive.current) return;
        setSummary(s);
        setError(false);
      })
      .catch(() => {
        if (alive.current) setError(true);
      });
  }, []);

  // Throttled: the first change fetches at once, a burst collapses into
  // one trailing fetch RELOAD_MIN_MS after the previous one.
  const requestReload = useCallback(() => {
    if (pendingTimer.current) return;
    const wait = lastFetch.current + RELOAD_MIN_MS - Date.now();
    if (wait <= 0) {
      void fetchNow();
      return;
    }
    pendingTimer.current = setTimeout(() => {
      pendingTimer.current = null;
      void fetchNow();
    }, wait);
  }, [fetchNow]);

  useEffect(() => {
    alive.current = true;
    let silence: ReturnType<typeof setTimeout> | null = null;
    let poll: ReturnType<typeof setInterval> | null = null;
    let lastKey: string | null = null;

    const stopPolling = () => {
      if (poll) clearInterval(poll);
      poll = null;
      setPolling(false);
    };
    const armSilence = () => {
      if (silence) clearTimeout(silence);
      silence = setTimeout(() => {
        if (!alive.current || poll) return;
        setPolling(true);
        poll = setInterval(() => void fetchNow(), SNAPSHOT_POLL_MS);
      }, SNAPSHOT_SILENCE_MS);
    };

    void fetchNow();
    armSilence();
    const off = subscribe("queues", (ev: ServerEvent) => {
      if (ev.type !== "queues.snapshot" || !ev.payload) return;
      stopPolling();
      armSilence();
      const key = stagesKey(ev.payload as unknown as QueuesSnapshot);
      if (lastKey !== null && key !== lastKey) requestReload();
      lastKey = key;
    });
    return () => {
      alive.current = false;
      off();
      if (silence) clearTimeout(silence);
      if (poll) clearInterval(poll);
      if (pendingTimer.current) clearTimeout(pendingTimer.current);
      pendingTimer.current = null;
    };
  }, [fetchNow, requestReload]);

  return { summary, error, polling, reload: () => void fetchNow() };
}
