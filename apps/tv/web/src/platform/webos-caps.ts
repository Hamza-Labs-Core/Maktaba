// Story 58.11 — what an LG TV plays, as the API's DeviceCaps (story 58.02). The answers come
// from the TV itself: webOS's device info (luna's system properties and config: the model,
// the SDK version, UHD, HDR10, Dolby Vision), MediaSource.isTypeSupported for each codec
// string below, and the native HLS player of webOS 5 and later (HLS v7, TS and fMP4
// segments; research.md §4.2). webosCaps() maps them; it is tested against
// shared/playback/caps-vectors.json, whose `answers` for a webOS TV have this shape.
//
// Dolby Vision in a web app is unreliable on LG (research.md §4.2): `dv-p8` is reported only
// when the TV says it supports Dolby Vision, isTypeSupported accepts profile 8, AND the
// model year is in DV_GOOD_YEARS, which the real-device checklist (docs/stores/lg.md, 58.11
// T05) fills. Otherwise the home sends the HDR10 base layer.

import type { DeviceCaps, VideoCap } from "./types";

/** The TV's own description (webOS.deviceInfo, or luna when webOSTV.js is absent). */
export interface WebOSDeviceInfo {
  modelName?: string;
  /** "7.1.0" on webOS 22 (2022). */
  sdkVersion?: string;
  uhd?: boolean;
  hdr10?: boolean;
  dolbyVision?: boolean;
}

/** The answers the caps are built from (caps-vectors.json's `answers` for webOS). */
export interface WebOSAnswers {
  device_info: WebOSDeviceInfo;
  /** isTypeSupported for each of PROBE_TYPES. */
  is_type_supported: Record<string, boolean>;
  native_hls: { version: number; fmp4: boolean };
}

export const TYPES = {
  h264: 'video/mp4; codecs="avc1.640033"',
  hevc: 'video/mp4; codecs="hvc1.2.4.L153.B0"',
  av1: 'video/mp4; codecs="av01.0.13M.10"',
  dvP8: 'video/mp4; codecs="dvh1.08.06"',
  aac: 'audio/mp4; codecs="mp4a.40.2"',
  ac3: 'audio/mp4; codecs="ac-3"',
  eac3: 'audio/mp4; codecs="ec-3"',
} as const;

/** Every codec string asked of the TV. */
export const PROBE_TYPES: readonly string[] = Object.values(TYPES);

/**
 * Model years whose Dolby Vision profile 8 a real TV has shown playing from the web app:
 * 2022 (an LG C2, webOS 22, recorded for 58.02's vectors). Each other year is added when
 * its T05 line in docs/stores/lg.md passes check 5.
 */
export const DV_GOOD_YEARS: readonly number[] = [2022];

/** The model year of a webOS TV from its SDK version: webOS 5 is 2020, 22 (SDK 7) 2022. */
export function yearFromSdk(sdkVersion: string | undefined): number {
  const major = Number((sdkVersion ?? "").split(".")[0]);
  if (!Number.isFinite(major) || major < 1) return 0;
  // webOS 5 (2020) on: one SDK major a year (webOS 22 is SDK 7, 25 is SDK 10); webOS 4.x
  // covered 2018 and 2019, 3.x 2016 and 2017 (the earlier year of each is named)
  if (major >= 5) return 2015 + major;
  return major === 4 ? 2018 : major === 3 ? 2016 : 0;
}

/** The native HLS player webOS has: v7 with fMP4 from webOS 5 (2020), TS only before. */
export function nativeHls(sdkVersion: string | undefined): { version: number; fmp4: boolean } {
  const major = Number((sdkVersion ?? "").split(".")[0]);
  return major >= 5 ? { version: 7, fmp4: true } : { version: 3, fmp4: false };
}

/** The DeviceCaps an LG TV reports from its answers. */
export function webosCaps(
  a: WebOSAnswers,
  dvGoodYears: readonly number[] = DV_GOOD_YEARS
): DeviceCaps {
  const ok = (t: string) => a.is_type_supported[t] === true;
  const info = a.device_info;
  const height = info.uhd ? 2160 : 1080;
  const video: VideoCap[] = [];
  // H.264 is every webOS TV's baseline, even when a probe cannot say so
  video.push({ codec: "h264", max_height: height, max_fps: 60 });
  if (ok(TYPES.hevc)) video.push({ codec: "hevc", max_height: height, max_fps: 60, bit_depth: 10 });
  if (ok(TYPES.av1)) video.push({ codec: "av1", max_height: height, max_fps: 60, bit_depth: 10 });

  const hdr: string[] = [];
  if (info.hdr10) hdr.push("hdr10", "hlg");
  if (info.dolbyVision && ok(TYPES.dvP8) && dvGoodYears.includes(yearFromSdk(info.sdkVersion)))
    hdr.push("dv-p8");

  const audio = ["aac"];
  const passthrough: string[] = [];
  if (ok(TYPES.ac3)) audio.push("ac3"), passthrough.push("ac3");
  if (ok(TYPES.eac3)) audio.push("eac3"), passthrough.push("eac3");

  return {
    containers: ["mp4", "mkv", "ts"],
    video,
    hdr,
    audio,
    passthrough,
    max_audio_channels: passthrough.length > 0 ? 6 : 2,
    hls_segments: a.native_hls.fmp4 ? ["ts", "fmp4"] : ["ts"],
    dash: false,
  };
}

/** isTypeSupported for each probe type: MediaSource's, else the <video>'s canPlayType. */
export function probeTypes(w: Window): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  const ms = (w as Window & { MediaSource?: { isTypeSupported?(t: string): boolean } }).MediaSource;
  let video: HTMLVideoElement | null = null;
  for (const t of PROBE_TYPES) {
    let yes = false;
    try {
      if (ms?.isTypeSupported) yes = ms.isTypeSupported(t);
      else {
        video = video ?? w.document.createElement("video");
        yes = video.canPlayType(t) !== "";
      }
    } catch {
      yes = false;
    }
    out[t] = yes;
  }
  return out;
}
