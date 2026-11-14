// Story 58.09 — what the launcher needs from the TV it runs on. Each platform (tizen.ts,
// webos.ts, browser.ts for tests and development) answers it; the player itself is the
// PlayerAdapter (player.ts), filled for AVPlay by 58.10 and for webOS's <video> by 58.11.

import type { BridgeAction } from "../bridge";

export type PlatformName = "tizen" | "webos" | "browser";

/** One video codec the TV decodes and its limits (the API's VideoCap, story 58.02). */
export interface VideoCap {
  codec: string;
  max_height?: number;
  max_fps?: number;
  bit_depth?: number;
}

/**
 * What the TV decodes, as reported to the home: the API's DeviceCaps (story 58.02,
 * api/internal/handlers/streaming/devicecaps.go), which the home merges with the platform's
 * ceiling. Each platform's reader is tested against shared/playback/caps-vectors.json.
 */
export interface DeviceCaps {
  containers: string[];
  video: VideoCap[];
  hdr: string[];
  audio: string[];
  passthrough: string[];
  max_audio_channels?: number;
  hls_segments: string[];
  dash?: boolean;
  max_bitrate_kbps?: number;
}

export interface DeviceInfo {
  model: string;
  /** The model year, when the platform says it (0 when unknown). */
  year: number;
  /** The web engine, e.g. "Chromium 108". */
  engine: string;
}

export interface Platform {
  readonly name: PlatformName;
  /** The model, year and engine. */
  device(): Promise<DeviceInfo>;
  /** The TV's own IPv4 address on the LAN (null when the platform cannot say). */
  localIp(): Promise<string | null>;
  /** Registers the remote's extra keys with the platform; returns their names. */
  registerKeys(): string[];
  /** The bridge action of a key the launcher's own page received (null: not one). */
  keyAction(e: KeyboardEvent): BridgeAction | null;
  /** What the TV decodes. */
  deviceCaps(): DeviceCaps;
  /** Leaves the app (after the UI's own confirm). */
  exit(): void;
}

/** The Chromium major version in a user agent (0 when there is none). */
export function chromiumMajor(ua: string): number {
  const m = /Chrom(?:e|ium)\/(\d+)/.exec(ua);
  if (m) return Number(m[1]);
  // a Samsung TV's web runtime names its engine without "Chrome/": "… (KHTML, like Gecko)
  // 69.0.3497.106.1/5.5 TV Safari/537.36" (the Chromium version, then the Tizen version)
  const t = /\)\s(\d{2,3})\.\d+\.\d+\.\d+(?:\.\d+)?\/[\d.]+\s+TV\b/.exec(ua);
  return t ? Number(t[1]) : 0;
}

/** The oldest engine the launcher supports: Chromium 68, a 2020 Samsung or LG (Q10). */
export const OLDEST_CHROMIUM = 68;

/** The engine of a user agent, for the hello answer. */
export function engineOf(ua: string): string {
  const major = chromiumMajor(ua);
  return major ? `Chromium ${major}` : "unknown";
}

/** The model year from the engine, as Samsung and LG ship it (research.md §3.1, §4.1). */
export function yearFromEngine(major: number, platform: PlatformName): number {
  const tizen: [number, number][] = [
    [130, 2026],
    [120, 2025],
    [108, 2024],
    [94, 2023],
    [85, 2022],
    [76, 2021],
    [69, 2020],
  ];
  const webos: [number, number][] = [
    [132, 2026],
    [120, 2025],
    [108, 2024],
    [94, 2023],
    [87, 2022],
    [79, 2021],
    [68, 2020],
  ];
  const table = platform === "tizen" ? tizen : platform === "webos" ? webos : [];
  for (const [engine, year] of table) if (major >= engine) return year;
  return 0;
}

/** Keys every remote sends that map to an action the same way on every TV. */
export function commonKeyAction(e: KeyboardEvent): BridgeAction | null {
  switch (e.keyCode) {
    case 10009: // Tizen Back
    case 461: // webOS Back
      return "back";
    case 10252:
      return "playPause";
    case 415:
      return "play";
    case 19:
      return "pause";
    case 413: // Stop: the player's pause, as on /tv/ (keys.ts)
      return "pause";
    case 417:
      return "seekForward";
    case 412:
      return "seekBack";
    case 403: // red: the menu key (the remotes have none)
      return "menu";
    default:
      break;
  }
  switch (e.key) {
    case "ArrowUp":
      return "up";
    case "ArrowDown":
      return "down";
    case "ArrowLeft":
      return "left";
    case "ArrowRight":
      return "right";
    case "Enter":
      return "select";
    case "Escape":
    case "XF86Back":
    case "GoBack":
      return "back";
    case "MediaPlayPause":
      return "playPause";
    default:
      return null;
  }
}

/** What any supported TV plays (Chromium 68, 2020): the report before a platform's own. */
export const DEFAULT_CAPS: DeviceCaps = {
  containers: ["mp4", "ts"],
  video: [{ codec: "h264", max_height: 1080, max_fps: 30 }],
  hdr: [],
  audio: ["aac"],
  passthrough: [],
  max_audio_channels: 2,
  hls_segments: ["ts"],
};

/** The default report with H.264 at the panel's height (a UHD panel: 2160). */
export function capsForPanel(uhd: boolean): DeviceCaps {
  return {
    ...DEFAULT_CAPS,
    video: [{ codec: "h264", max_height: uhd ? 2160 : 1080, max_fps: 30 }],
  };
}
