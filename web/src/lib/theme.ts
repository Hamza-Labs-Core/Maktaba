// Theme controller (Story 11.8).
//
// Three modes: "light" | "dark" | "system". The *resolved* attribute
// stamped on <html data-theme> is only ever "light" or "dark" — tokens.css
// keys off that. "system" defers to `prefers-color-scheme` and tracks
// live OS changes via a matchMedia listener.
//
// First paint: a blocking same-origin script, web/public/theme-boot.js,
// loaded from index.html's <head> applies the resolved attribute BEFORE the
// JS bundle parses, so there is no flash of the wrong theme on a cold/slow
// load (the SPA's CSP forbids inline script, so it cannot be inline). This
// module keeps the runtime in sync afterwards.
//
// Precedence: explicit localStorage choice > OS preference > "dark".
// Only an OS that explicitly prefers light resolves "system" to light; no
// preference (or no matchMedia) resolves to dark.
// EC (11.8): a corrupted/unknown stored value falls back to "system".
const STORAGE_KEY = "mkt:theme";

export type ThemeMode = "light" | "dark" | "system";
export type ResolvedTheme = "light" | "dark";

export function readMode(): ThemeMode {
  let stored: string | null = null;
  try {
    stored = typeof localStorage !== "undefined" ? localStorage.getItem(STORAGE_KEY) : null;
  } catch {
    // storage blocked — behave as if nothing is stored.
  }
  if (stored === "light" || stored === "dark" || stored === "system") return stored;
  // EC: corrupted/absent key → "system" (NOT a hardcoded light).
  return "system";
}

const LIGHT_QUERY = "(prefers-color-scheme: light)";

// The OS asks for light explicitly; anything else (dark, no preference,
// no matchMedia) is dark.
function systemPrefersLight(): boolean {
  return typeof matchMedia !== "undefined" && matchMedia(LIGHT_QUERY).matches;
}

export function resolveTheme(mode: ThemeMode = readMode()): ResolvedTheme {
  if (mode === "light" || mode === "dark") return mode;
  return systemPrefersLight() ? "light" : "dark";
}

export function applyResolvedTheme(mode: ThemeMode = readMode()): ResolvedTheme {
  const resolved = resolveTheme(mode);
  document.documentElement.dataset.theme = resolved;
  return resolved;
}

export function setMode(mode: ThemeMode): ResolvedTheme {
  try {
    localStorage.setItem(STORAGE_KEY, mode);
  } catch {
    // locked-down browsers — choice is session-only.
  }
  return applyResolvedTheme(mode);
}

// Subscribe to OS theme changes; only re-applies while in "system" mode.
// Returns an unsubscribe. No-op where matchMedia is unavailable.
export function watchSystemTheme(onChange?: (t: ResolvedTheme) => void): () => void {
  if (typeof matchMedia === "undefined") return () => {};
  const mq = matchMedia(LIGHT_QUERY);
  const handler = () => {
    if (readMode() === "system") {
      const r = applyResolvedTheme("system");
      onChange?.(r);
    }
  };
  mq.addEventListener("change", handler);
  return () => mq.removeEventListener("change", handler);
}
