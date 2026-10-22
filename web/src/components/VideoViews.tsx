// Shared pieces of the video lists (Library, Search): the Grid / List
// toggle remembered in localStorage, the poster with its 404 fallback and
// the h:mm:ss duration.
import { useEffect, useState } from "react";
import { useI18n } from "../lib/i18n";

export type View = "grid" | "list";

/** The view stored under `key`, or `fallback` (storage may be blocked). */
export function readStoredView(key: string, fallback: View): View {
  try {
    const v = localStorage.getItem(key);
    if (v === "grid" || v === "list") return v;
  } catch {
    /* ignored */
  }
  return fallback;
}

/** A view kept in localStorage under `key`. */
export function useStoredView(key: string, fallback: View) {
  const [view, setView] = useState<View>(() => readStoredView(key, fallback));
  useEffect(() => {
    try {
      localStorage.setItem(key, view);
    } catch {
      /* ignored */
    }
  }, [key, view]);
  return [view, setView] as const;
}

/** The Grid / List segmented control (a radiogroup). */
export function ViewToggle({ view, onChange }: { view: View; onChange: (v: View) => void }) {
  const { t } = useI18n();
  return (
    <div className="mkt-segmented" role="radiogroup" aria-label={t("library.view")}>
      <button
        type="button"
        role="radio"
        aria-checked={view === "grid"}
        onClick={() => onChange("grid")}
      >
        {t("library.view.grid")}
      </button>
      <button
        type="button"
        role="radio"
        aria-checked={view === "list"}
        onClick={() => onChange("list")}
      >
        {t("library.view.list")}
      </button>
    </div>
  );
}

/** A video's poster; a missing one (404) shows a plain box. */
export function VideoPoster({ videoId, src }: { videoId: string; src?: string }) {
  const [broken, setBroken] = useState(false);
  if (broken) {
    return <div className="mkt-card__poster mkt-card__poster--fallback" aria-hidden />;
  }
  return (
    <img
      className="mkt-card__poster"
      src={src || `/api/videos/${videoId}/poster`}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  );
}

/** Seconds as m:ss, or h:mm:ss from an hour up. */
export function formatDuration(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);
  const mm = m.toString().padStart(2, "0");
  const ss = s.toString().padStart(2, "0");
  return h > 0 ? `${h}:${mm}:${ss}` : `${m}:${ss}`;
}
