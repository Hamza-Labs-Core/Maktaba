// Switch telemetry (Epic 34.6 AC9): each switch emits
// switch{ms_to_card, ms_to_first_frame, path, prefetched} to a sink the host
// app supplies; the admin usage panel (the public site, 34.9) shows the
// p50/p75/p95 of these.
import type { SwitchEvent } from "./switcher";

export type TelemetrySink = (e: SwitchEvent) => void;

/** The q-th percentile (0..1) of values (nearest rank); NaN for none. */
export function percentile(values: number[], q: number): number {
  if (values.length === 0) return NaN;
  const s = [...values].sort((a, b) => a - b);
  const rank = Math.min(s.length - 1, Math.max(0, Math.ceil(q * s.length) - 1));
  return s[rank];
}

/** A sink that keeps the last `max` events in memory (the Maktaba web app). */
export class SwitchLog {
  readonly events: SwitchEvent[] = [];
  constructor(private readonly max = 100) {}
  sink: TelemetrySink = (e) => {
    this.events.push(e);
    if (this.events.length > this.max) this.events.shift();
  };
  summary(): { p50: number; p75: number; p95: number; n: number } {
    const v = this.events.map((e) => e.ms_to_first_frame);
    return {
      p50: percentile(v, 0.5),
      p75: percentile(v, 0.75),
      p95: percentile(v, 0.95),
      n: v.length,
    };
  }
}
