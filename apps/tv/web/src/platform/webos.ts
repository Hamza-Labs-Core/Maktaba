// Stories 58.09 and 58.11 — LG webOS. The packaged app speaks to luna (webos-luna.ts): the
// connection manager names the TV's address for the subnet probe, the system properties and
// config describe the model (webOS.deviceInfo when webOSTV.js is loaded). webOS delivers the
// color and media keys without registration; Back is key code 461, and leaving goes back to
// the webOS launcher through platformBack (appinfo.json's disableBackHistoryAPI hands Back
// to the app). The player is the TV's own <video> (webos-player.ts), and what it plays is
// reported as the API's DeviceCaps (webos-caps.ts).

import {
  chromiumMajor,
  commonKeyAction,
  engineOf,
  yearFromEngine,
  type DeviceCaps,
  type DeviceInfo,
  type Platform,
} from "./types";
import { log } from "../log";
import { luna, readDeviceInfo } from "./webos-luna";
import { nativeHls, probeTypes, webosCaps, yearFromSdk, type WebOSDeviceInfo } from "./webos-caps";
import { WebOSPlayer } from "./webos-player";

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

/** The keys webOS sends the page without registration (their names, for hello). */
export const WEBOS_KEYS = [
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

export function webosPlatform(w: Window): Platform {
  let info: WebOSDeviceInfo = {};
  return {
    name: "webos",
    async device(): Promise<DeviceInfo> {
      const ua = w.navigator.userAgent;
      info = await readDeviceInfo(w);
      const year = yearFromSdk(info.sdkVersion) || yearFromEngine(chromiumMajor(ua), "webos");
      log(
        "launcher",
        `webos: sdk ${info.sdkVersion ?? "?"} uhd ${!!info.uhd} hdr10 ${!!info.hdr10} dv ${!!info.dolbyVision}`
      );
      return { model: info.modelName || "LG TV", year, engine: engineOf(ua) };
    },
    async localIp(): Promise<string | null> {
      const res = await luna(w, "luna://com.webos.service.connectionmanager", "getStatus");
      return res ? ipFromStatus(res) : null;
    },
    registerKeys(): string[] {
      // webOS sends the color and media keys to the page as they are
      return WEBOS_KEYS.slice();
    },
    keyAction: commonKeyAction,
    deviceCaps(): DeviceCaps {
      // the SDK version names the native HLS player; a TV that does not say is webOS 5+
      // (older ones are refused before this, by their engine)
      const sdk = info.sdkVersion ?? "5";
      return webosCaps({
        device_info: info,
        is_type_supported: probeTypes(w),
        native_hls: nativeHls(sdk),
      });
    },
    player(video: HTMLVideoElement) {
      return new WebOSPlayer(video, (url) =>
        w.fetch(url, { credentials: "omit", cache: "no-store" })
      );
    },
    exit() {
      try {
        if (w.webOS?.platformBack) w.webOS.platformBack();
        else if (w.PalmSystem?.platformBack) w.PalmSystem.platformBack();
        else w.close();
      } catch (e) {
        log("error", `webos: exit failed: ${String(e)}`);
      }
    },
  };
}
