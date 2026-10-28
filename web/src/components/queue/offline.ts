// Epic 47 helpers shared by the Queues page's offline pieces (the library
// menu, the sources dialog, the file-actions and duplicates pages).
import { useCallback } from "react";
import { useI18n } from "../../lib/i18n";

/** A library action of the Queues page, in pipeline order: download, convert, dedup. */
export type OfflineAction = "youtube_refresh" | "convert" | "dedup";
export const OFFLINE_ACTIONS: OfflineAction[] = ["youtube_refresh", "convert", "dedup"];

/** Localised byte sizes: 512 B, 1.5 KB, 3.2 GB. */
export function useBytes(): (b: number | null | undefined) => string {
  const { t, locale } = useI18n();
  return useCallback(
    (b: number | null | undefined) => {
      if (b === null || b === undefined || !Number.isFinite(b)) return "–";
      const units = ["B", "KB", "MB", "GB", "TB"] as const;
      let v = Math.max(0, b);
      let i = 0;
      while (v >= 1024 && i < units.length - 1) {
        v /= 1024;
        i++;
      }
      const num = new Intl.NumberFormat(locale === "ar" ? "ar-EG" : "en", {
        maximumFractionDigits: i === 0 ? 0 : 1,
      }).format(v);
      return t(`queue.unit.bytes.${units[i]}`, { n: num });
    },
    [t, locale]
  );
}

/** A localised calendar date (no time). */
export function useDate(): (iso: string | null | undefined) => string {
  const { locale } = useI18n();
  return useCallback(
    (iso: string | null | undefined) =>
      iso
        ? new Intl.DateTimeFormat(locale === "ar" ? "ar-EG" : "en", {
            year: "numeric",
            month: "short",
            day: "numeric",
          }).format(new Date(iso))
        : "–",
    [locale]
  );
}
