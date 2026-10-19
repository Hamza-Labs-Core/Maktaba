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

export function useClusterFmt(): ClusterFmt {
  const { t, locale } = useI18n();
  const loc = locale === "ar" ? "ar-EG" : "en";
  const reason = useCallback(
    (code: string) => {
      const [head, ...rest] = String(code).split(":");
      const key = `machines.reason.${head.trim()}`;
      const word = t(key);
      const detail = rest.join(":").trim();
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
