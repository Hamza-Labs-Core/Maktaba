// Theme controller (Story 11.8).
//
// Three modes: "light" | "dark" | "system". The *resolved* attribute
// stamped on <html data-theme> is only ever "light" or "dark" — tokens.css
// keys off that. "system" defers to `prefers-color-scheme` and tracks
// live OS changes via a matchMedia listener.
//
// First paint: an inline blocking script in index.html (see THEME_BOOT)
// applies the resolved attribute BEFORE the JS bundle parses, so there
// is no flash of the wrong theme on a cold/slow load. This module keeps
// the runtime in sync afterwards.
//
// Precedence: explicit localStorage choice > OS preference > "light".
// EC (11.8): a corrupted/unknown stored value falls back to "system".
const STORAGE_KEY = "mkt:theme";

export type ThemeMode = "light" | "dark" | "system";
export type ResolvedTheme = "light" | "dark";

export function readMode(): ThemeMode {
  if (typeof localStorage !== "undefined") {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark" || stored === "system") return stored;
  }
  // EC: corrupted/absent key → "system" (NOT a hardcoded light).
  return "system";
}

function systemPrefersDark(): boolean {
  return typeof matchMedia !== "undefined" && matchMedia("(prefers-color-scheme: dark)").matches;
}

export function resolveTheme(mode: ThemeMode = readMode()): ResolvedTheme {
  if (mode === "light" || mode === "dark") return mode;
  return systemPrefersDark() ? "dark" : "light";
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
  const mq = matchMedia("(prefers-color-scheme: dark)");
  const handler = () => {
    if (readMode() === "system") {
      const r = applyResolvedTheme("system");
      onChange?.(r);
    }
  };
  mq.addEventListener("change", handler);
  return () => mq.removeEventListener("change", handler);
}

// Inline boot snippet injected verbatim into index.html <head> so the
// resolved theme is on <html> before first paint (no FOUC). Kept tiny
// and dependency-free; mirrors readMode()/resolveTheme() logic.
export const THEME_BOOT = `(function(){try{var m=localStorage.getItem('mkt:theme');if(m!=='light'&&m!=='dark'&&m!=='system')m='system';var d=m==='dark'||(m==='system'&&window.matchMedia&&window.matchMedia('(prefers-color-scheme: dark)').matches);document.documentElement.dataset.theme=d?'dark':'light';}catch(e){document.documentElement.dataset.theme='light';}})();`;
