// The setup screens' words follow the machine the app runs on (Epic 38 story
// 38.6): "this Mac" and the menu bar on a Mac, "this PC" and the tray on
// Windows, "this computer" elsewhere. The shell says which (app_state's
// platform); an older shell that does not is a Mac's (Epic 36).
import { useEffect, useState } from "react";
import type { Platform, SetupApi } from "../../lib/desktopSetup";

export function usePlatform(api: SetupApi): Platform {
  const [platform, setPlatform] = useState<Platform>("macos");
  useEffect(() => {
    let alive = true;
    api
      .appState()
      .then((s) => {
        if (alive && s.platform) setPlatform(s.platform);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [api]);
  return platform;
}

/** The {machine} and {tray} words of the setup strings for a platform. */
export function platformWords(t: (key: string) => string, p: Platform) {
  return { machine: t(`setup.machine.${p}`), tray: t(`setup.tray.${p}`) };
}
