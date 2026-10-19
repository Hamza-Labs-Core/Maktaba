// Remote TV playback for the web player (Epic 32 story 32.9).
//
//   POST   /api/tv/remote/tune                    {channel_id, device} → 200 live | 202 starting
//                                                 | 402 budget_exhausted | 403 remote_not_allowed
//                                                 | 404 no_such_channel | 429 user_streams_full
//                                                 | remote_channels_full | 503 remote_unavailable
//   POST   /api/tv/remote/sessions/{id}/heartbeat → {state, expires_at, token?, hls_url?}
//   DELETE /api/tv/remote/sessions/{id}
//
// A RemoteSession is a Stream for the shared TV player (@maktaba/tvplayer):
// it tunes (retrying a 202 with the same session until the push is live),
// beats every 60 s and swaps the signed token without reloading the media —
// hls.js rewrites the token path segment of every playlist and segment URL
// to the newest token (AC4); Safari (native HLS) gets the new URL and the
// player swaps it in on its hidden element. It ends on release (channel
// change, stop) and on page unload (a keepalive DELETE).
import { ApiError, api, deleteBeacon } from "./api";
import { SwitchError, type ResolveContext, type Stream } from "@maktaba/tvplayer";

export const HEARTBEAT_MS = 60_000;
export const START_TIMEOUT_MS = 90_000;
export const CF_HOST_SUFFIX = ".cloudflarestream.com";

export interface RemoteTuned {
  session_id: string;
  state: "live" | "starting" | string;
  hls_url?: string;
  expires_at?: string;
  refresh_after?: string;
  retry_after?: number;
}

export interface RemoteBeat {
  state: "live" | "starting" | "ended" | string;
  expires_at?: string;
  token?: string;
  hls_url?: string;
}

const sess = (id: string) => `/api/tv/remote/sessions/${encodeURIComponent(id)}`;

export const remoteTvApi = {
  tune: (channelId: string, device: string) =>
    api.post<RemoteTuned>("/api/tv/remote/tune", { channel_id: channelId, device }),
  heartbeat: (id: string) => api.post<RemoteBeat>(`${sess(id)}/heartbeat`),
  end: (id: string) => api.delete<void>(sess(id)),
  endOnUnload: (id: string) => deleteBeacon(sess(id)),
};

/** The signed token of a Cloudflare playback URL (its first path segment). */
export function tokenOf(url: string): string | null {
  try {
    const u = new URL(url);
    if (!u.hostname.endsWith(CF_HOST_SUFFIX)) return null;
    const seg = u.pathname.split("/")[1];
    return seg || null;
  } catch {
    return null;
  }
}

/**
 * Puts `token` in the token path segment of a Cloudflare playback URL
 * (`https://customer-x.cloudflarestream.com/<token>/manifest/video.m3u8`,
 * and every playlist and segment under it). Any other URL, and every other
 * segment, is left alone.
 */
export function rewriteToken(url: string, token: string): string {
  if (!token) return url;
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return url;
  }
  if (!u.hostname.endsWith(CF_HOST_SUFFIX)) return url;
  const parts = u.pathname.split("/");
  if (parts.length < 3 || !parts[1]) return url;
  parts[1] = token;
  u.pathname = parts.join("/");
  return u.toString();
}

/** A short device label for the admin page ("Chrome on Windows"). */
export function deviceLabel(
  ua: string = typeof navigator !== "undefined" ? navigator.userAgent : ""
): string {
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /Firefox\//.test(ua)
      ? "Firefox"
      : /Chrome\//.test(ua)
        ? "Chrome"
        : /Safari\//.test(ua)
          ? "Safari"
          : "Browser";
  const os = /iPhone|iPad/.test(ua)
    ? "iOS"
    : /Android/.test(ua)
      ? "Android"
      : /Mac OS X/.test(ua)
        ? "macOS"
        : /Windows/.test(ua)
          ? "Windows"
          : /Linux/.test(ua)
            ? "Linux"
            : "";
  return os ? `${browser} on ${os}` : browser;
}

/** The error code of a refused tune or heartbeat. */
export function remoteCode(e: unknown): string {
  if (e instanceof ApiError) {
    const code = (e.problem as unknown as { code?: string }).code;
    if (code) return code;
    switch (e.status) {
      case 402:
        return "budget_exhausted";
      case 403:
        return "remote_not_allowed";
      case 404:
        return "no_such_channel";
      case 429:
        return "user_streams_full";
      case 503:
        return "remote_unavailable";
    }
    return `http_${e.status}`;
  }
  return "network";
}

/** A refused tune as the switcher's error (the card shows its words). */
export function toSwitchError(e: unknown): SwitchError {
  const code = remoteCode(e);
  const detail = e instanceof ApiError ? (e.problem.detail ?? "") : "";
  // a network blip or a 5xx is worth another try; a refusal is not
  const retryable = code === "network" || code.startsWith("http_5");
  return new SwitchError(code, detail || code, retryable);
}

function wait(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(t);
      resolve();
    });
  });
}

export interface RemoteOpenOptions {
  device?: string;
  /** The session ended on the server (410/404 on a heartbeat). */
  onEnded?: () => void;
  heartbeatMs?: number;
  startTimeoutMs?: number;
}

/** A live remote session: a Stream with a token that heartbeats refresh. */
export class RemoteSession implements Stream {
  readonly path = "remote";
  url: string;
  private token: string;
  private beat: ReturnType<typeof setInterval> | null = null;
  private ended = false;
  private listeners = new Set<(url: string) => void>();
  private readonly unload = () => this.endNow(true);

  constructor(
    readonly id: string,
    hlsUrl: string,
    private readonly o: RemoteOpenOptions = {}
  ) {
    this.url = hlsUrl;
    this.token = tokenOf(hlsUrl) ?? "";
    this.beat = setInterval(() => void this.heartbeat(), o.heartbeatMs ?? HEARTBEAT_MS);
    if (typeof window !== "undefined") window.addEventListener("pagehide", this.unload);
  }

  /** Tunes channelId through the remote API (202 → the same session again until live). */
  static async open(
    channelId: string,
    ctx: Pick<ResolveContext, "signal" | "status" | "freeSlot">,
    o: RemoteOpenOptions = {}
  ): Promise<RemoteSession> {
    const device = o.device ?? deviceLabel();
    const deadline = Date.now() + (o.startTimeoutMs ?? START_TIMEOUT_MS);
    let freed = false;
    let sessionId: string | null = null;
    const giveBack = () => {
      if (sessionId) void remoteTvApi.end(sessionId).catch(() => {});
    };
    for (;;) {
      if (ctx.signal.aborted) {
        giveBack();
        throw new SwitchError("aborted");
      }
      let r: RemoteTuned;
      try {
        r = await remoteTvApi.tune(channelId, device);
      } catch (e) {
        // the per-user limit with this tab's own old session holding a slot:
        // let the old channel go first (break before make), then tune again
        if (!freed && remoteCode(e) === "user_streams_full") {
          freed = true;
          ctx.freeSlot();
          await wait(250, ctx.signal);
          continue;
        }
        throw toSwitchError(e);
      }
      sessionId = r.session_id;
      if (r.state === "live" && r.hls_url) {
        if (ctx.signal.aborted) {
          giveBack();
          throw new SwitchError("aborted");
        }
        return new RemoteSession(r.session_id, r.hls_url, o);
      }
      ctx.status("remote_starting");
      if (Date.now() > deadline) {
        giveBack();
        throw new SwitchError("remote_timeout", "the remote stream did not start", true);
      }
      await wait(Math.max(1, r.retry_after ?? 3) * 1000, ctx.signal);
    }
  }

  /** hls.js loader hook: the newest token in every Cloudflare URL. */
  rewrite = (url: string): string => rewriteToken(url, this.token);

  onUrl(cb: (url: string) => void): () => void {
    this.listeners.add(cb);
    return () => this.listeners.delete(cb);
  }

  get currentToken(): string {
    return this.token;
  }

  async heartbeat(): Promise<void> {
    if (this.ended) return;
    try {
      const b = await remoteTvApi.heartbeat(this.id);
      if (this.ended) return;
      if (b.token || b.hls_url) {
        if (b.hls_url) this.url = b.hls_url;
        this.token = b.token || tokenOf(this.url) || this.token;
        for (const cb of this.listeners) cb(this.url);
      }
    } catch (e) {
      const code = remoteCode(e);
      if (code === "session_ended" || code === "no_such_session" || code === "remote_not_allowed") {
        this.stop();
        this.o.onEnded?.();
      }
      // anything else: the next beat tries again (the server allows 150 s)
    }
  }

  release(): void {
    this.endNow(false);
  }

  private stop() {
    this.ended = true;
    if (this.beat) clearInterval(this.beat);
    this.beat = null;
    this.listeners.clear();
    if (typeof window !== "undefined") window.removeEventListener("pagehide", this.unload);
  }

  private endNow(unloading: boolean) {
    if (this.ended) return;
    this.stop();
    if (unloading) remoteTvApi.endOnUnload(this.id);
    else void remoteTvApi.end(this.id).catch(() => {});
  }
}
