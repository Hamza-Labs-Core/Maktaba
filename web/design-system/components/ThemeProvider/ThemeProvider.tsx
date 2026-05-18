import { createContext, useContext, useEffect, type ReactNode } from "react";

// Story 17.2 AC: theme context with a `system` default. The actual
// resolution lives in the OS / browser via prefers-color-scheme; this
// provider only stamps a `data-theme` attribute on <html> so the CSS
// custom-property cascade in tokens.css can flip semantic colours.

export type Theme = "light" | "dark" | "system";

interface ThemeContextValue {
  theme: Theme;
  inProvider: boolean;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export interface ThemeProviderProps {
  theme?: Theme;
  children: ReactNode;
}

export function ThemeProvider({ theme = "system", children }: ThemeProviderProps) {
  useEffect(() => {
    if (typeof document === "undefined") return;
    const root = document.documentElement;
    if (theme === "system") {
      delete root.dataset.theme;
    } else {
      root.dataset.theme = theme;
    }
  }, [theme]);
  return (
    <ThemeContext.Provider value={{ theme, inProvider: true }}>{children}</ThemeContext.Provider>
  );
}

// Story 17.2 EC: components used outside ThemeProvider warn in dev,
// fall back to defaults in prod.
export function useTheme(componentName?: string): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) {
    if (import.meta.env?.DEV) {
      const tag = componentName ?? "component";
      console.warn(`<${tag}> rendered outside <ThemeProvider>; using system theme.`);
    }
    return { theme: "system", inProvider: false };
  }
  return ctx;
}
