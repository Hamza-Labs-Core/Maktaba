// Stories 58.09 and 58.10 — Samsung Tizen. The packaged app (apps/tv/web/tizen/config.xml)
// has `tizen` and `webapis`: webapis.network.getIp() for the subnet probe,
// webapis.productinfo for the model, tizen.tvinputdevice to register the remote's media and
// color keys (unregistered, they never reach a page), tizen.application to leave (Back,
// 10009, after the UI's confirm), the device caps from webapis (tizen/caps.ts) and the
// player, webapis.avplay (tizen/avplay.ts), with Maktaba's own subtitle layer.

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
import { t } from "../i18n";
import { VideoElementPlayer, type PlayerAdapter } from "../player";
import { SubtitleLayer, parseVtt } from "../subtitles";
import { AvplayPlayer, type Avplay } from "./tizen/avplay";
import { probeTizen, tizenCaps, type TizenCapsApis } from "./tizen/caps";
import "./tizen/tizen.css";

interface TizenApi {
  tvinputdevice?: {
    registerKeyBatch?: (keys: string[]) => void;
    registerKey?: (k: string) => void;
  };
  application?: { getCurrentApplication(): { exit(): void } };
  tvaudiocontrol?: { getOutputMode?(): string };
}

interface WebApis {
  network?: { getIp(): string };
  productinfo?: {
    getModel?(): string;
    getRealModel?(): string;
    isUdPanelSupported?(): boolean;
    is8KPanelSupported?(): boolean;
  };
  avinfo?: TizenCapsApis["avinfo"];
  avplay?: Avplay;
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
      const year = yearFromEngine(chromiumMajor(w.navigator.userAgent), "tizen");
      const apis: TizenCapsApis = {
        productinfo: w.webapis?.productinfo,
        avinfo: w.webapis?.avinfo,
        audiocontrol: w.tizen?.tvaudiocontrol,
      };
      const caps = tizenCaps(probeTizen(w, apis, year));
      log(
        "launcher",
        `tizen caps: ${caps.video.map((v) => v.codec).join(",")} ${caps.hdr.join(",") || "sdr"} passthrough ${caps.passthrough.join(",") || "none"}`
      );
      return caps;
    },
    player(video: HTMLVideoElement): PlayerAdapter {
      return tizenPlayer(w, video);
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

/** How long the playback notice stays over the video. */
export const NOTICE_MS = 5000;

/**
 * AVPlay with Maktaba's subtitle layer, in the launcher's stage: AVPlay's <object> under
 * the iframe, the subtitles and the notice above it. Without webapis.avplay (an emulator,
 * research.md §3.3) the launcher's <video> plays instead.
 */
export function tizenPlayer(w: Window, video: HTMLVideoElement): PlayerAdapter {
  const doc = w.document;
  const stage = video.parentElement ?? doc.body;
  const av = w.webapis?.avplay;
  if (!av) {
    log("launcher", "tizen: no webapis.avplay, playing in <video>");
    return new VideoElementPlayer(video);
  }
  const plane = doc.createElement("object");
  plane.setAttribute("type", "application/avplayer");
  plane.className = "ml-avplay-plane";
  plane.hidden = true;
  stage.insertBefore(plane, stage.firstChild);
  const subtitles = new SubtitleLayer(stage);
  const notice = doc.createElement("div");
  notice.className = "ml-notice";
  notice.setAttribute("role", "status");
  notice.setAttribute("data-testid", "player-notice");
  notice.hidden = true;
  stage.appendChild(notice);
  let timer: ReturnType<typeof setTimeout> | null = null;
  let uhd = false;
  try {
    uhd = !!w.webapis?.productinfo?.isUdPanelSupported?.();
  } catch {
    uhd = false;
  }
  return new AvplayPlayer({
    avplay: av,
    doc,
    plane,
    subtitles,
    parseVtt,
    fetchText: async (url) => {
      const res = await w.fetch(url, { credentials: "omit", cache: "no-store" });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      return res.text();
    },
    uhd,
    notice: () => {
      notice.textContent = t("tizen.error.playback");
      notice.dir = doc.documentElement.dir || "ltr";
      notice.hidden = false;
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => (notice.hidden = true), NOTICE_MS);
    },
  });
}
