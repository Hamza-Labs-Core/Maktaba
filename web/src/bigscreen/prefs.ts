// Epic 46 — big screen's playback preferences, kept in this browser
// (localStorage; a blocked storage keeps the defaults).
import { useCallback, useState } from "react";

export interface BigPrefs {
  /** At a lesson's end, play the series' next one after a 10 s countdown. */
  autoplayNext: boolean;
  /** Turn the first subtitle track on when a video starts. */
  subtitlesOn: boolean;
  /** Subtitle size, a factor of the player's default. */
  subtitleScale: number;
}

export const SUBTITLE_SCALES = [0.75, 1, 1.25, 1.5, 2] as const;
export const DEFAULT_PREFS: BigPrefs = {
  autoplayNext: true,
  subtitlesOn: false,
  subtitleScale: 1.25,
};
const KEY = "mkt:big:prefs";

export function readPrefs(): BigPrefs {
  try {
    const raw = JSON.parse(localStorage.getItem(KEY) || "{}") as Partial<BigPrefs>;
    return {
      autoplayNext:
        typeof raw.autoplayNext === "boolean" ? raw.autoplayNext : DEFAULT_PREFS.autoplayNext,
      subtitlesOn:
        typeof raw.subtitlesOn === "boolean" ? raw.subtitlesOn : DEFAULT_PREFS.subtitlesOn,
      subtitleScale: (SUBTITLE_SCALES as readonly number[]).includes(raw.subtitleScale as number)
        ? (raw.subtitleScale as number)
        : DEFAULT_PREFS.subtitleScale,
    };
  } catch {
    return { ...DEFAULT_PREFS };
  }
}

export function writePrefs(p: BigPrefs) {
  try {
    localStorage.setItem(KEY, JSON.stringify(p));
  } catch {
    /* ignored */
  }
}

export function usePrefs() {
  const [prefs, setPrefs] = useState<BigPrefs>(readPrefs);
  const update = useCallback((patch: Partial<BigPrefs>) => {
    setPrefs((cur) => {
      const nextPrefs = { ...cur, ...patch };
      writePrefs(nextPrefs);
      return nextPrefs;
    });
  }, []);
  return [prefs, update] as const;
}
