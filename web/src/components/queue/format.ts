// Pure helpers shared by the queue components: ring maths, queue state
// classification and localised duration / rate formatting.
import { useCallback, useMemo } from "react";
import { useI18n } from "../../lib/i18n";
import type { QueueSummary, StageInfo } from "../../lib/queueApi";

export type QueueVisualState = "running" | "failing" | "waiting" | "done" | "off";

export const STATE_GLYPH: Record<QueueVisualState, string> = {
  running: "▶",
  failing: "!",
  waiting: "…",
  done: "✓",
  off: "–",
};

/** Fraction of a ring that is filled, clamped to 0..1 (empty total => 0). */
export function ringFraction(done: number, total: number): number {
  if (!(total > 0)) return 0;
  return Math.min(1, Math.max(0, done / total));
}

/** Arc length for an SVG circle ring (stroke-dasharray first value). */
export function ringDash(done: number, total: number, radius: number): [number, number] {
  const c = 2 * Math.PI * radius;
  return [ringFraction(done, total) * c, c];
}

// running > failing > waiting > done. A queue that is running keeps the
// running colour even with a few failures (the failure count is shown as
// a separate flag), so a healthy queue is not painted red.
export function queueVisualState(info: StageInfo | undefined, q: QueueSummary): QueueVisualState {
  if (info && !info.enabled) return "off";
  // A switched-off or not-yet-configured queue is "off", never "failing".
  if (q.waiting_reason === "not_configured" || q.waiting_reason === "disabled") return "off";
  // Epic 40: red only when the API says one new error dominates; a few failed items
  // (or jobs parked on the model, a share, disk space) never paint a queue red
  if (q.health) {
    if (q.health.state === "failing") return "failing";
    if (q.counts.running > 0) return "running";
    if (q.health.state === "waiting") return "waiting";
  } else {
    if (q.counts.running > 0) return "running";
    if (q.counts.failed_open > 0) return "failing";
  }
  const live = q.counts.pending + q.counts.paused + q.counts.awaiting_review;
  if (live > 0 || info?.paused) return "waiting";
  return "done";
}

export function percent(done: number, total: number): number {
  return Math.round(ringFraction(done, total) * 100);
}

export interface Fmt {
  /** seconds -> "45 s" / "8 min" / "2 h 5 min" / "3 d" */
  dur: (sec: number | null | undefined) => string;
  ms: (ms: number | null | undefined) => string;
  rate: (perMin: number) => string;
  ago: (iso: string | null | undefined) => string;
  /** elapsed time since an instant, as a duration (no "ago"). */
  since: (iso: string | null | undefined) => string;
  clock: (d: Date) => string;
}

export function useFmt(): Fmt {
  const { t, n, locale } = useI18n();
  const dur = useCallback(
    (sec: number | null | undefined) => {
      if (sec === null || sec === undefined || !Number.isFinite(sec)) return "–";
      const s = Math.max(0, Math.round(sec));
      if (s < 60) return t("queue.unit.sec", { n: n(s) });
      const m = Math.round(s / 60);
      if (m < 60) return t("queue.unit.min", { n: n(m) });
      const h = Math.floor(m / 60);
      if (h < 24) {
        const rest = m % 60;
        return rest
          ? t("queue.unit.hourMin", { h: n(h), m: n(rest) })
          : t("queue.unit.hour", { n: n(h) });
      }
      return t("queue.unit.day", { n: n(Math.floor(h / 24)) });
    },
    [t, n]
  );
  return useMemo(
    () => ({
      dur,
      ms: (ms) =>
        ms === null || ms === undefined
          ? "–"
          : ms < 1000
            ? t("queue.unit.ms", { n: n(Math.round(ms)) })
            : dur(ms / 1000),
      rate: (perMin) =>
        t("queue.unit.perMin", {
          n: new Intl.NumberFormat(locale === "ar" ? "ar-EG" : "en", {
            maximumFractionDigits: perMin < 10 ? 1 : 0,
          }).format(perMin),
        }),
      ago: (iso) => {
        if (!iso) return "–";
        const sec = (Date.now() - new Date(iso).getTime()) / 1000;
        return t("queue.unit.ago", { t: dur(Math.max(0, sec)) });
      },
      since: (iso) => (iso ? dur(Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000)) : "–"),
      clock: (d) =>
        new Intl.DateTimeFormat(locale === "ar" ? "ar-EG" : "en", {
          hour: "2-digit",
          minute: "2-digit",
          second: "2-digit",
        }).format(d),
    }),
    [dur, t, n, locale]
  );
}
