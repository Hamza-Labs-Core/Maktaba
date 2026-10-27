// Back to where the viewer came from: the desktop and TV apps' windows have no
// browser Back. A page opened from a pasted link (no in-app history) goes to
// `fallback` instead of leaving the app, in that first entry's place and still
// first: its own Back goes on to its fallback, never back and forth between two
// pages (the player and the video page).
import { useLocation, useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../lib/i18n";

export function BackButton({ fallback, className }: { fallback: string; className?: string }) {
  const nav = useNavigate();
  const location = useLocation();
  const { t, dir } = useI18n();
  const first =
    location.key === "default" || (location.state as { first?: boolean } | null)?.first === true;
  return (
    <Button
      variant="ghost"
      className={className}
      onClick={() => (first ? nav(fallback, { replace: true, state: { first: true } }) : nav(-1))}
    >
      {dir === "rtl" ? "→" : "←"} {t("player.back")}
    </Button>
  );
}
