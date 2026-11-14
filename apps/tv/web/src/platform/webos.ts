// Story 58.09 — LG webOS. The packaged app (58.11) loads webOSTV.js, which gives
// `webOS.service.request` (luna services: the connection manager names the TV's address
// for the subnet probe), `webOS.deviceInfo` for the model, and `webOS.platformBack` to
// leave. webOS delivers the color and media keys without registration. The native <video>
// HLS player is 58.11's adapter.

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

interface LunaRequest {
  method: string;
  parameters?: Record<string, unknown>;
  onSuccess?: (res: Record<string, unknown>) => void;
  onFailure?: (err: Record<string, unknown>) => void;
}

interface WebOSApi {
  service?: { request(uri: string, req: LunaRequest): unknown };
  deviceInfo?: (
    cb: (info: { modelName?: string; version?: string; uhd?: boolean }) => void
  ) => void;
  platformBack?: () => void;
}

declare global {
  interface Window {
    webOS?: WebOSApi;
    PalmSystem?: unknown;
  }
}

export function isWebOS(w: Window): boolean {
  return !!w.webOS || !!w.PalmSystem;
}

/** The connection manager's address: wired first, then Wi-Fi. */
export function ipFromStatus(res: Record<string, unknown>): string | null {
  for (const k of ["wired", "wifi"]) {
    const n = res[k] as { state?: string; ipAddress?: string } | undefined;
    if (n && n.state === "connected" && n.ipAddress) return n.ipAddress;
  }
  return null;
}

export function webosPlatform(w: Window): Platform {
  let uhd = false;
  return {
    name: "webos",
    device(): Promise<DeviceInfo> {
      const ua = w.navigator.userAgent;
      const base = { year: yearFromEngine(chromiumMajor(ua), "webos"), engine: engineOf(ua) };
      return new Promise((resolve) => {
        const info = w.webOS?.deviceInfo;
        if (!info) {
          resolve({ model: "LG TV", ...base });
          return;
        }
        try {
          info((d) => {
            uhd = !!d.uhd;
            resolve({ model: d.modelName || "LG TV", ...base });
          });
        } catch (e) {
          log("error", `webos: deviceInfo failed: ${String(e)}`);
          resolve({ model: "LG TV", ...base });
        }
      });
    },
    localIp(): Promise<string | null> {
      return new Promise((resolve) => {
        const svc = w.webOS?.service;
        if (!svc) {
          resolve(null);
          return;
        }
        try {
          svc.request("luna://com.webos.service.connectionmanager", {
            method: "getStatus",
            onSuccess: (res) => resolve(ipFromStatus(res)),
            onFailure: (err) => {
              log("error", `webos: connectionmanager failed: ${JSON.stringify(err)}`);
              resolve(null);
            },
          });
        } catch (e) {
          log("error", `webos: connectionmanager failed: ${String(e)}`);
          resolve(null);
        }
      });
    },
    registerKeys(): string[] {
      // webOS sends the color and media keys to the page as they are
      return ["MediaPlayPause", "MediaPlay", "MediaPause", "MediaStop", "ColorF0Red"];
    },
    keyAction: commonKeyAction,
    deviceCaps(): DeviceCaps {
      // the full report (HEVC, AV1, Dolby Vision, passthrough) is story 58.11's
      return { ...DEFAULT_CAPS, max_height: uhd ? 2160 : 1080 };
    },
    exit() {
      try {
        if (w.webOS?.platformBack) w.webOS.platformBack();
        else w.close();
      } catch (e) {
        log("error", `webos: exit failed: ${String(e)}`);
      }
    },
  };
}
