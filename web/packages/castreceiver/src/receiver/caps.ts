// What this Cast device plays, as story 58.02's `device_caps`
// (docs/developers/contributing/playback.md): asked of the device with CAF's
// `canDisplayType` and its device capabilities, never assumed from the model. A Chromecast
// 3rd gen answers H.264 at 1080p only, so every HEVC source is a transcode for it, with no
// special case here. The home clamps what the report asks to the `cast` profile's ceiling.

import type { CafContext, CafFramework } from "./caf";

export interface VideoCap {
  codec: string;
  max_height: number;
  max_fps?: number;
  bit_depth?: number;
}

export interface DeviceCaps {
  containers: string[];
  video: VideoCap[];
  hdr: string[];
  audio: string[];
  passthrough: string[];
  max_audio_channels: number;
  hls_segments: string[];
}

// The probes, tallest first: the first that answers yes is the codec's ceiling.
const VIDEO: {
  codec: string;
  probes: { codecs: string; height: number; width: number; depth?: number }[];
}[] = [
  {
    codec: "h264",
    probes: [
      { codecs: "avc1.640033", width: 3840, height: 2160 },
      { codecs: "avc1.640028", width: 1920, height: 1080 },
      { codecs: "avc1.4d401f", width: 1280, height: 720 },
    ],
  },
  {
    codec: "hevc",
    probes: [
      { codecs: "hev1.2.4.L153.B0", width: 3840, height: 2160, depth: 10 },
      { codecs: "hev1.1.6.L153.B0", width: 3840, height: 2160 },
      { codecs: "hev1.2.4.L120.B0", width: 1920, height: 1080, depth: 10 },
      { codecs: "hev1.1.6.L120.B0", width: 1920, height: 1080 },
    ],
  },
  {
    codec: "av1",
    probes: [
      { codecs: "av01.0.12M.10", width: 3840, height: 2160, depth: 10 },
      { codecs: "av01.0.08M.08", width: 1920, height: 1080 },
    ],
  },
];

const AUDIO: { name: string; codecs: string }[] = [
  { name: "aac", codecs: "mp4a.40.2" },
  { name: "ac3", codecs: "ac-3" },
  { name: "eac3", codecs: "ec-3" },
];

function capability(
  ctx: CafContext,
  fw: CafFramework | undefined,
  name: string,
  fallback: string
): boolean {
  let all: Record<string, unknown> | null | undefined;
  try {
    all = ctx.getDeviceCapabilities?.();
  } catch {
    all = null;
  }
  const key = fw?.system?.DeviceCapabilities?.[name] ?? fallback;
  return all?.[key] === true;
}

function can(
  ctx: CafContext,
  mime: string,
  codecs: string,
  width?: number,
  height?: number,
  fps?: number
): boolean {
  try {
    return ctx.canDisplayType(mime, codecs, width, height, fps) === true;
  } catch {
    return false;
  }
}

/** The device's report, from its own answers. */
export function deviceCaps(ctx: CafContext, fw?: CafFramework): DeviceCaps {
  const video: VideoCap[] = [];
  for (const v of VIDEO) {
    const hit = v.probes.find((p) => can(ctx, "video/mp4", p.codecs, p.width, p.height));
    if (!hit) continue;
    const cap: VideoCap = { codec: v.codec, max_height: hit.height };
    if (can(ctx, "video/mp4", hit.codecs, hit.width, hit.height, 60)) cap.max_fps = 60;
    if (hit.depth) cap.bit_depth = hit.depth;
    video.push(cap);
  }
  const hdr: string[] = [];
  const hdrScreen = capability(ctx, fw, "IS_HDR_SUPPORTED", "is_hdr_supported");
  const tenBit = video.some((v) => v.codec === "hevc" && v.bit_depth === 10);
  if (hdrScreen && tenBit) hdr.push("hdr10", "hlg");
  if (capability(ctx, fw, "IS_DV_SUPPORTED", "is_dv_supported")) {
    if (can(ctx, "video/mp4", "dvh1.05.06", 3840, 2160)) hdr.push("dv-p5");
    if (can(ctx, "video/mp4", "dvh1.08.06", 3840, 2160)) hdr.push("dv-p8");
  }
  const audio = AUDIO.filter((a) => can(ctx, "audio/mp4", a.codecs)).map((a) => a.name);
  const atmos = capability(ctx, fw, "IS_DOLBY_ATMOS_SUPPORTED", "is_dolby_atmos_supported");
  const passthrough = atmos ? ["eac3"] : [];
  return {
    containers: ["mp4", "ts"],
    video,
    hdr,
    audio,
    passthrough,
    max_audio_channels: atmos || audio.includes("eac3") || audio.includes("ac3") ? 8 : 2,
    hls_segments: ["ts", "fmp4"],
  };
}

/** The HLS segment format of a session (58.02: HEVC and AV1 in fMP4, everything else in TS),
 * from the codec of the rendition it plays. */
export function segmentFormat(session: {
  current_rendition?: { codec?: string } | null;
  ladder?: { codec?: string }[] | null;
}): "TS" | "FMP4" {
  const codec = (
    session.current_rendition?.codec ||
    session.ladder?.[0]?.codec ||
    ""
  ).toLowerCase();
  return /^(hevc|h265|hvc1|hev1|av1|av01)/.test(codec) ? "FMP4" : "TS";
}
