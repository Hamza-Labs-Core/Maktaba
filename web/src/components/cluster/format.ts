// Localised number formatting for the cluster views: bytes, rates and
// percentages through Intl units (so Arabic gets its digits and unit
// names), plus the reason-code wording the Machines page shares.
import { useCallback, useMemo } from "react";
import { useI18n } from "../../lib/i18n";
import type { FilesTarget } from "../../lib/clusterApi";

const UNITS = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"] as const;

function scale(v: number): [number, (typeof UNITS)[number]] {
  let i = 0;
  let x = Math.abs(v);
  while (x >= 1024 && i < UNITS.length - 1) {
    x /= 1024;
    i++;
  }
  return [Math.sign(v) * x, UNITS[i]];
}

export interface ClusterFmt {
  /** 3.2 GB */
  bytes: (v: number | null | undefined) => string;
  /** 1.2 MB/s */
  rate: (bytesPerSec: number | null | undefined) => string;
  /** 42% (input 0..100) */
  pct: (v: number | null | undefined) => string;
  /** a plain number with at most `digits` decimals */
  num: (v: number | null | undefined, digits?: number) => string;
  /** 14:03:22 */
  time: (ms: number) => string;
  /** "transcription" for a reason code, "cannot run here: no GPU" for "unavailable: no GPU" */
  reason: (code: string) => string;
  /** where a process saves its files */
  files: (f: FilesTarget | null | undefined) => string | null;
}

/**
 * The words behind a "paused: ..." reason (a machine whose code does not fit the home's
 * database schema, Epic 31 design §5.1). The pipeline and the streaming node write them
 * in English so that an older home's page still shows something readable; this page
 * translates the two forms it knows.
 */
export function pausedReason(detail: string): { key: string; vars: Record<string, string> } | null {
  let m = /^home database at (\d+), this machine needs (\d+)/.exec(detail);
  if (m) return { key: "machines.reason.pausedHomeBehind", vars: { home: m[1], need: m[2] } };
  m = /needs this machine at (\d+) or newer \(it has (\d+)\)/.exec(detail);
  if (m) return { key: "machines.reason.pausedNodeBehind", vars: { need: m[1], have: m[2] } };
  return null;
}

/** The "paused: ..." reason of a machine's workers, or null when it runs. */
export function pausedOf(
  workers: { stages_off?: Record<string, string> }[] | undefined
): string | null {
  for (const w of workers ?? []) {
    for (const r of Object.values(w.stages_off ?? {})) {
      if (typeof r === "string" && r.startsWith("paused:")) return r;
    }
  }
  return null;
}

export function useClusterFmt(): ClusterFmt {
  const { t, locale } = useI18n();
  const loc = locale === "ar" ? "ar-EG" : "en";
  const reason = useCallback(
    (code: string) => {
      const [head, ...rest] = String(code).split(":");
      const key = `machines.reason.${head.trim()}`;
      const detail = rest.join(":").trim();
      if (head.trim() === "paused") {
        const why = pausedReason(detail);
        if (why) return t(why.key, why.vars);
      }
      const word = t(key);
      if (word === key) return code;
      return detail ? `${word}: ${detail}` : word;
    },
    [t]
  );
  return useMemo(() => {
    const unit = (v: number, u: string, digits: number) =>
      new Intl.NumberFormat(loc, {
        style: "unit",
        unit: u,
        unitDisplay: "short",
        maximumFractionDigits: digits,
      }).format(v);
    const ok = (v: number | null | undefined): v is number =>
      typeof v === "number" && Number.isFinite(v);
    return {
      bytes: (v) => {
        if (!ok(v)) return "–";
        const [x, u] = scale(v);
        return unit(x, u, x < 10 ? 1 : 0);
      },
      rate: (v) => {
        if (!ok(v)) return "–";
        const [x, u] = scale(v);
        return unit(x, `${u}-per-second`, x < 10 ? 1 : 0);
      },
      pct: (v) =>
        ok(v)
          ? new Intl.NumberFormat(loc, { style: "percent", maximumFractionDigits: 0 }).format(
              v / 100
            )
          : "–",
      num: (v, digits = 1) =>
        ok(v) ? new Intl.NumberFormat(loc, { maximumFractionDigits: digits }).format(v) : "–",
      time: (ms) =>
        new Intl.DateTimeFormat(loc, {
          hour: "2-digit",
          minute: "2-digit",
          second: "2-digit",
        }).format(new Date(ms)),
      reason,
      files: (f) => {
        if (!f) return null;
        if (f.on_share) return t("cluster.files.onShare");
        return f.reason
          ? t("cluster.files.localWhy", { reason: reason(f.reason) })
          : t("cluster.files.local");
      },
    };
  }, [loc, reason, t]);
}
