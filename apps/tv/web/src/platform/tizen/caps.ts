// Story 58.10 — what a Samsung TV plays, for the home (story 58.02's device_caps).
//
// Two steps, so the mapping is tested against shared/playback/caps-vectors.json (whose
// Samsung vectors record these answers on real models):
//
//   probeTizen(w)      asks the TV: webapis.productinfo for the panel (UHD, 8K), the
//                      decoder through MediaSource.isTypeSupported / canPlayType (AVPlay and
//                      MSE share the TV's hardware decoder), webapis.avinfo for HDR, and
//                      webapis.audiocontrol's output mode for what passes through to a
//                      soundbar or receiver;
//   tizenCaps(answers) maps those answers to the report.
//
// Rules the mapping keeps (story 58.10):
//   - no Dolby Vision on Samsung TVs: `dv-*` is never reported, so the home sends HDR10 or
//     HLG (a DV profile 8.1 file is remuxed with its DV stripped);
//   - HDR10+ is not a source format Maktaba keeps, so it is not reported;
//   - passthrough is what the TV's audio output takes: a soundbar that cannot take EAC3
//     puts the TV in Dolby Digital mode, the report drops eac3 and the home converts it;
//   - VP9 and MP3 are not formats the home sends a TV, so they are left out.

import type { DeviceCaps, VideoCap } from "../types";
import { log } from "../../log";

/** A Samsung TV's answers, in the shape caps-vectors.json records them. */
export interface TizenAnswers {
  productinfo: {
    isUdPanelSupported: boolean;
    is8KPanelSupported: boolean;
    /** The model year, when known (0 otherwise); the report does not depend on it. */
    firmware_year: number;
  };
  /** What AVPlay opens: containers and adaptive formats. */
  avplay_formats: string[];
  video: string[];
  /** "HDR10", "HLG", "HDR10+" as the TV names them. */
  hdr: string[];
  audio: string[];
  audio_output: { passthrough: string[]; max_channels: number };
}

/** What AVPlay opens on every supported Samsung TV (2020 and later, research.md §3.2). */
export const AVPLAY_FORMATS = ["mp4", "mkv", "ts", "hls", "dash"];

/** The video codecs the home sends a TV, and the MIME each is probed with. */
const VIDEO_PROBES: [string, string][] = [
  ["h264", 'video/mp4; codecs="avc1.640033"'],
  ["hevc", 'video/mp4; codecs="hvc1.2.4.L153.B0"'],
  ["av1", 'video/mp4; codecs="av01.0.13M.10"'],
  ["vp9", 'video/webm; codecs="vp09.00.50.08"'],
];

const AUDIO_PROBES: [string, string][] = [
  ["aac", 'audio/mp4; codecs="mp4a.40.2"'],
  ["ac3", 'audio/mp4; codecs="ac-3"'],
  ["eac3", 'audio/mp4; codecs="ec-3"'],
  ["mp3", "audio/mpeg"],
];

const CONTAINERS = ["mp4", "mkv", "ts"];
const VIDEO = ["h264", "hevc", "av1"];
const AUDIO = ["aac", "ac3", "eac3"];
const HDR: Record<string, string> = { HDR10: "hdr10", HLG: "hlg" };

/** The report for a Samsung TV's answers. */
export function tizenCaps(a: TizenAnswers): DeviceCaps {
  const height = a.productinfo.is8KPanelSupported
    ? 4320
    : a.productinfo.isUdPanelSupported
      ? 2160
      : 1080;
  const video: VideoCap[] = VIDEO.filter((c) => a.video.indexOf(c) >= 0).map((codec) =>
    codec === "h264"
      ? { codec, max_height: height, max_fps: 60 }
      : { codec, max_height: height, max_fps: 60, bit_depth: 10 }
  );
  const hdr: string[] = [];
  for (const h of a.hdr) {
    const name = HDR[h];
    if (name && hdr.indexOf(name) < 0) hdr.push(name);
  }
  const hls = a.avplay_formats.indexOf("hls") >= 0;
  return {
    containers: CONTAINERS.filter((c) => a.avplay_formats.indexOf(c) >= 0),
    video,
    hdr,
    audio: AUDIO.filter((c) => a.audio.indexOf(c) >= 0),
    passthrough: AUDIO.filter((c) => a.audio_output.passthrough.indexOf(c) >= 0),
    max_audio_channels: a.audio_output.max_channels,
    hls_segments: hls ? ["ts", "fmp4"] : [],
    dash: a.avplay_formats.indexOf("dash") >= 0,
  };
}

/** The Tizen web APIs the probe reads (each may be missing on an older firmware). */
export interface TizenCapsApis {
  productinfo?: {
    isUdPanelSupported?(): boolean;
    is8KPanelSupported?(): boolean;
  };
  avinfo?: { isHdrTvSupport?(): boolean };
  audiocontrol?: { getOutputMode?(): string };
}

/**
 * What the TV's audio output takes, from webapis.audiocontrol.getOutputMode(): "PCM",
 * "DOLBY" (Dolby Digital: AC3), "DOLBY_DIGITAL_PLUS" (AC3 and EAC3), "DTS", "AAC". Only
 * Dolby passes through to the home's streams (DTS is converted: 58.02).
 */
export function audioOutput(mode: string | null): TizenAnswers["audio_output"] {
  switch ((mode || "").toUpperCase()) {
    case "DOLBY_DIGITAL_PLUS":
      return { passthrough: ["ac3", "eac3"], max_channels: 6 };
    case "DOLBY":
      return { passthrough: ["ac3"], max_channels: 6 };
    default:
      return { passthrough: [], max_channels: 2 };
  }
}

function safe<T>(what: string, fn: () => T, fallback: T): T {
  try {
    return fn();
  } catch (e) {
    log("error", `tizen caps: ${what} failed: ${String(e)}`);
    return fallback;
  }
}

/** Whether the TV's decoder takes a MIME type (MSE first, then a <video>'s canPlayType). */
function decodes(w: Window, mime: string): boolean {
  const ms = (w as Window & { MediaSource?: { isTypeSupported?(t: string): boolean } })
    .MediaSource;
  if (ms && ms.isTypeSupported && ms.isTypeSupported(mime)) return true;
  const v = w.document.createElement("video");
  return !!v.canPlayType && v.canPlayType(mime) !== "";
}

/** Asks the TV (see the header). */
export function probeTizen(w: Window, apis: TizenCapsApis | undefined, year: number): TizenAnswers {
  const pi = apis?.productinfo;
  const uhd = safe("isUdPanelSupported", () => !!pi?.isUdPanelSupported?.(), false);
  const k8 = safe("is8KPanelSupported", () => !!pi?.is8KPanelSupported?.(), false);
  const hdrTv = safe("isHdrTvSupport", () => !!apis?.avinfo?.isHdrTvSupport?.(), false);
  const mode = safe("getOutputMode", () => apis?.audiocontrol?.getOutputMode?.() ?? null, null);
  return {
    productinfo: { isUdPanelSupported: uhd, is8KPanelSupported: k8, firmware_year: year },
    avplay_formats: AVPLAY_FORMATS.slice(),
    video: VIDEO_PROBES.filter(([, m]) => safe(m, () => decodes(w, m), false)).map(([c]) => c),
    hdr: hdrTv ? ["HDR10", "HLG"] : [],
    audio: AUDIO_PROBES.filter(([, m]) => safe(m, () => decodes(w, m), false)).map(([c]) => c),
    audio_output: audioOutput(mode),
  };
}
