// Story 58.09 — the platform the launcher runs on: Tizen when `tizen` and `webapis` exist,
// webOS when `webOS` (or the older PalmSystem) does, else a plain browser.

import { browserPlatform } from "./browser";
import { isTizen, tizenPlatform } from "./tizen";
import { isWebOS, webosPlatform } from "./webos";
import type { Platform } from "./types";

export function detectPlatform(w: Window): Platform {
  if (isTizen(w)) return tizenPlatform(w);
  if (isWebOS(w)) return webosPlatform(w);
  return browserPlatform(w);
}

export type { Platform, PlatformName, DeviceCaps, DeviceInfo } from "./types";
