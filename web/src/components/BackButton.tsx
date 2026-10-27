// Back to where the viewer came from: the desktop and TV apps' windows have no
// browser Back. A page opened from a pasted link (no in-app history) goes to
// `fallback` instead of leaving the app.
import { useLocation, useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../lib/i18n";

export function BackButton({ fallback, className }: { fallback: string; className?: string }) {
  const nav = useNavigate();
  const location = useLocation();
  const { t, dir } = useI18n();
  return (
    <Button
      variant="ghost"
      className={className}
      onClick={() => (location.key === "default" ? nav(fallback) : nav(-1))}
    >
      {dir === "rtl" ? "→" : "←"} {t("player.back")}
    </Button>
  );
}
