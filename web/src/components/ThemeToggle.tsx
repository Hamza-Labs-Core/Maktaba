import { useState } from "react";
import { resolveTheme, setTheme, type Theme } from "../lib/theme";

export function ThemeToggle() {
  const [theme, setLocalTheme] = useState<Theme>(resolveTheme);
  const next: Theme = theme === "light" ? "dark" : "light";
  return (
    <button
      type="button"
      className="mkt-btn mkt-btn--ghost"
      onClick={() => {
        setTheme(next);
        setLocalTheme(next);
      }}
      aria-label={`Switch to ${next} theme`}
    >
      {theme === "light" ? "🌙" : "☀"}
    </button>
  );
}
