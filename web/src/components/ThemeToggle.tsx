// Quick theme cycle (light → dark → system) in the header. The
// authoritative picker with explicit radios lives in Settings →
// Appearance; this is the one-tap convenience affordance.
import { useState } from "react";
import { readMode, setMode, type ThemeMode } from "../lib/theme";
import { useI18n } from "../lib/i18n";

const ORDER: ThemeMode[] = ["light", "dark", "system"];
const GLYPH: Record<ThemeMode, string> = { light: "☀", dark: "🌙", system: "◐" };

export function ThemeToggle() {
  const { t } = useI18n();
  const [mode, setLocalMode] = useState<ThemeMode>(readMode);
  const next = ORDER[(ORDER.indexOf(mode) + 1) % ORDER.length];
  return (
    <button
      type="button"
      className="mkt-btn mkt-btn--ghost"
      onClick={() => {
        setMode(next);
        setLocalMode(next);
      }}
      aria-label={t("settings.theme") + ": " + t(`settings.theme.${next}`)}
      title={t(`settings.theme.${mode}`)}
    >
      {GLYPH[mode]}
    </button>
  );
}
