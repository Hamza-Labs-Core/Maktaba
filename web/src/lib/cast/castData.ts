// What a Maktaba sender hands Maktaba's Cast receiver (Epic 58 story 58.06): the custom data of
// the Cast load request (MediaInfo.customData, never a header). The rules are
// shared/cast/load-vectors.json, which the Android and Apple senders run too; the contract is
// docs/developers/contributing/casting.md.

export type CastLang = "en" | "ar";

export interface CastLoadInput {
  /** The address this sender reaches the home at (its own origin, for the web). */
  senderBase: string;
  /** The home's base URLs from GET /api/discovery/home: the LAN ones first, the relay last. */
  discoveryUrls: readonly string[];
  grant: string;
  videoId?: string;
  queueId?: string;
  queuePos?: number;
  startSec: number;
  lang: string;
  apiLevel: number;
}

export interface CastData {
  home: string;
  urls: string[];
  grant: string;
  video_id?: string;
  queue_id?: string;
  queue_pos?: number;
  start_sec: number;
  lang: CastLang;
  api_level: number;
}

const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;

/**
 * The URL without its trailing slash when a TV can load it: HTTPS (a receiver page is HTTPS,
 * and a browser blocks plain-HTTP media from it), and not localhost, a .local name or a bare IP
 * (no certificate names those, so the TV's check would fail); else null.
 */
export function tvReachable(raw: string): string | null {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return null;
  }
  if (u.protocol !== "https:") return null;
  const host = u.hostname.toLowerCase().replace(/\.$/, "");
  if (!host || host === "localhost" || host.endsWith(".localhost") || host.endsWith(".local"))
    return null;
  if (IPV4.test(host) || host.startsWith("[") || host.includes(":")) return null;
  return `${u.protocol}//${u.host}${u.pathname}`.replace(/\/+$/, "");
}

/** The load's custom data, or null when the home has no address a TV can reach. */
export function castData(input: CastLoadInput): CastData | null {
  const urls: string[] = [];
  for (const raw of [...input.discoveryUrls, input.senderBase]) {
    const u = tvReachable(raw);
    if (u && !urls.includes(u)) urls.push(u);
  }
  if (urls.length === 0) return null;
  const out: CastData = {
    home: urls[0],
    urls,
    grant: input.grant,
    start_sec: Math.max(0, Math.floor(Number.isFinite(input.startSec) ? input.startSec : 0)),
    lang: input.lang === "ar" ? "ar" : "en",
    api_level: input.apiLevel,
  };
  if (input.queueId) {
    out.queue_id = input.queueId;
    if (input.queuePos !== undefined && input.queuePos >= 0) out.queue_pos = input.queuePos;
  } else if (input.videoId) {
    out.video_id = input.videoId;
  }
  return out;
}
