// Epic 58 story 58.09 — whether /tv/ runs in the Samsung or LG launcher, and how. The big
// screen's Settings read it: Switch home (the launcher's home picker) and the plain-HTTP
// line show only there.
import { createContext, useContext } from "react";

export interface LauncherMode {
  /** The --bundle-ui build: the page is the package's, the API at `base`. */
  bundled: boolean;
  /** The home's address when bundled (null: the page's own origin is the home's). */
  base: string | null;
  /** The home is reached over plain HTTP on the LAN: sign-in by code only, and said so. */
  plain: boolean;
  /** The launcher's language at start. */
  lang: "en" | "ar" | null;
}

export const LauncherContext = createContext<LauncherMode | null>(null);

export function useLauncher(): LauncherMode | null {
  return useContext(LauncherContext);
}
