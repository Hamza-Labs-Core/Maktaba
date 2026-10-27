// Back to where the viewer came from: the desktop and TV apps' windows have no
// browser Back. A page opened from a pasted link (no in-app history) goes to
// `fallback` instead of leaving the app, in that first entry's place and still
// first: its own Back goes on to its fallback, never back and forth between two
// pages (the player and the video page).
import { useLocation, useNavigate, type Location } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../lib/i18n";

/** Whether the page is the app's first in this window: nothing in-app to go back to. */
function isFirst(location: Location): boolean {
  if (location.key === "default") return true;
  if ((location.state as { first?: boolean } | null)?.first === true) return true;
  // the browser router's own index, kept across a replace
  try {
    return (window.history.state as { idx?: number } | null)?.idx === 0;
  } catch {
    return false;
  }
}

/**
 * keepFirst is the state a page's own replace (a tab in its query) carries, so
 * that a first page stays first for its Back.
 */
export function keepFirst(location: Location): unknown {
  if (!isFirst(location)) return location.state;
  return { ...((location.state as object | null) ?? {}), first: true };
}

export function BackButton({ fallback, className }: { fallback: string; className?: string }) {
  const nav = useNavigate();
  const location = useLocation();
  const { t, dir } = useI18n();
  return (
    <Button
      variant="ghost"
      className={className}
      onClick={() =>
        isFirst(location) ? nav(fallback, { replace: true, state: { first: true } }) : nav(-1)
      }
    >
      {dir === "rtl" ? "→" : "←"} {t("player.back")}
    </Button>
  );
}
