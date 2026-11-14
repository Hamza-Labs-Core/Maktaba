// Story 58.09 — a desktop browser, for development and the tests: no TV APIs. The TV's
// address comes from the page's `?ip=` (the Playwright tests set it), and leaving the app
// is recorded on the page (document.body.dataset.exited) so a test can see it.

import {
  DEFAULT_CAPS,
  commonKeyAction,
  engineOf,
  type DeviceCaps,
  type DeviceInfo,
  type Platform,
} from "./types";

export function browserPlatform(w: Window): Platform {
  return {
    name: "browser",
    async device(): Promise<DeviceInfo> {
      return { model: "Browser", year: 0, engine: engineOf(w.navigator.userAgent) };
    },
    async localIp(): Promise<string | null> {
      return new URLSearchParams(w.location.search).get("ip");
    },
    registerKeys(): string[] {
      return [];
    },
    keyAction: commonKeyAction,
    deviceCaps(): DeviceCaps {
      return { ...DEFAULT_CAPS, video: DEFAULT_CAPS.video.slice() };
    },
    exit() {
      w.document.body.dataset.exited = "true";
    },
  };
}
