// Epic 46 — the header's way into big-screen mode (/big): remembers this page
// for the way back and asks for full screen (lib/bigScreen).
import { useLocation, useNavigate } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import { BIG_BASE, enterBigScreen } from "../lib/bigScreen";

export function BigScreenButton() {
  const { t } = useI18n();
  const nav = useNavigate();
  const { pathname, search } = useLocation();
  return (
    <button
      type="button"
      className="mkt-btn mkt-btn--ghost mkt-shell__big"
      title={`${t("big.enter")} (g b)`}
      aria-label={t("big.enter")}
      data-testid="enter-big-screen"
      onClick={() => {
        enterBigScreen(pathname + search);
        nav(BIG_BASE);
      }}
    >
      <span aria-hidden="true">⛶</span> {t("big.enter.short")}
    </button>
  );
}
