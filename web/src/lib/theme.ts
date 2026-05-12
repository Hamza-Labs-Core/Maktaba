// Theme toggle (Story 11.8).
//
// `applyInitialTheme()` runs synchronously in main.tsx before React
// mounts so the first paint already has the right `data-theme` attr —
// no flash of wrong colors.
//
// Order of precedence:
//   1. localStorage (`mkt:theme`) — explicit user choice wins
//   2. system preference via `prefers-color-scheme`
//   3. fall back to "light"
//
// `data-theme="dark"` swaps the CSS-variable values defined in
// tokens.css. Components NEVER hard-code colors.
const STORAGE_KEY = "mkt:theme";
export type Theme = "light" | "dark";

export function applyInitialTheme(): Theme {
  const t = resolveTheme();
  document.documentElement.dataset.theme = t;
  return t;
}

export function resolveTheme(): Theme {
  if (typeof localStorage !== "undefined") {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark") return stored;
  }
  if (typeof matchMedia !== "undefined") {
    if (matchMedia("(prefers-color-scheme: dark)").matches) return "dark";
  }
  return "light";
}

export function setTheme(t: Theme): void {
  document.documentElement.dataset.theme = t;
  try {
    localStorage.setItem(STORAGE_KEY, t);
  } catch {
    // SSR / locked-down browsers — silently skip persistence.
  }
}
