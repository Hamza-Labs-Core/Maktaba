// Story 58.09 — Samsung Tizen. The packaged app (58.10) has `tizen` and `webapis`:
// webapis.network.getIp() for the subnet probe, webapis.productinfo for the model,
// tizen.tvinputdevice to register the remote's media and color keys (unregistered, they
// never reach a page), and tizen.application to leave. AVPlay is 58.10's player adapter.

import {
  DEFAULT_CAPS,
  chromiumMajor,
  commonKeyAction,
  engineOf,
  yearFromEngine,
  type DeviceCaps,
  type DeviceInfo,
  type Platform,
} from "./types";
import { log } from "../log";

interface TizenApi {
  tvinputdevice?: { registerKeyBatch?: (keys: string[]) => void; registerKey?: (k: string) => void };
  application?: { getCurrentApplication(): { exit(): void } };
}

interface WebApis {
  network?: { getIp(): string };
  productinfo?: {
    getModel?(): string;
    getRealModel?(): string;
    isUdPanelSupported?(): boolean;
  };
}

declare global {
  interface Window {
    tizen?: TizenApi;
    webapis?: WebApis;
  }
}

/** The keys registered on Tizen (their names in tizen.tvinputdevice). */
export const TIZEN_KEYS = [
  "MediaPlayPause",
  "MediaPlay",
  "MediaPause",
  "MediaStop",
  "MediaFastForward",
  "MediaRewind",
  "ColorF0Red",
  "ColorF1Green",
  "ColorF2Yellow",
  "ColorF3Blue",
];

export function isTizen(w: Window): boolean {
  return !!w.tizen && !!w.webapis;
}

export function tizenPlatform(w: Window): Platform {
  return {
    name: "tizen",
    async device(): Promise<DeviceInfo> {
      const pi = w.webapis?.productinfo;
      let model = "Samsung TV";
      try {
        model = pi?.getRealModel?.() || pi?.getModel?.() || model;
      } catch (e) {
        log("error", `tizen: productinfo failed: ${String(e)}`);
      }
      const ua = w.navigator.userAgent;
      return { model, year: yearFromEngine(chromiumMajor(ua), "tizen"), engine: engineOf(ua) };
    },
    async localIp(): Promise<string | null> {
      try {
        return w.webapis?.network?.getIp() || null;
      } catch (e) {
        log("error", `tizen: getIp failed: ${String(e)}`);
        return null;
      }
    },
    registerKeys(): string[] {
      const dev = w.tizen?.tvinputdevice;
      if (!dev) return [];
      try {
        if (dev.registerKeyBatch) dev.registerKeyBatch(TIZEN_KEYS);
        else TIZEN_KEYS.forEach((k) => dev.registerKey?.(k));
        return TIZEN_KEYS.slice();
      } catch (e) {
        log("error", `tizen: key registration failed: ${String(e)}`);
        return [];
      }
    },
    keyAction: commonKeyAction,
    deviceCaps(): DeviceCaps {
      let uhd = false;
      try {
        uhd = !!w.webapis?.productinfo?.isUdPanelSupported?.();
      } catch {
        uhd = false;
      }
      // the rest of the report (HEVC, AV1, HDR, passthrough) is AVPlay's, story 58.10
      return { ...DEFAULT_CAPS, max_height: uhd ? 2160 : 1080 };
    },
    exit() {
      try {
        w.tizen?.application?.getCurrentApplication().exit();
      } catch (e) {
        log("error", `tizen: exit failed: ${String(e)}`);
      }
    },
  };
}
