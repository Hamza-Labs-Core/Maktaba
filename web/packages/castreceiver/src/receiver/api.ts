// The home's routes a cast grant may call (docs/developers/contributing/casting.md, "What a
// grant may do"), with the grant as the bearer. The receiver has no API of its own: these are
// the routes every player uses.

import type { DeviceCaps } from "./caps";

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

export interface Session {
  session_id: string;
  mode: string;
  manifest_url?: string;
  direct_url?: string;
  expires_at: string;
  duration_sec?: number;
  start_sec?: number;
  current_rendition?: { codec?: string; height?: number } | null;
  ladder?: { codec?: string }[] | null;
  /** The token for the session's own paths (story 58.02): sidecar subtitles, chapters. */
  stream_sig?: string;
}

export interface SubtitleTrack {
  language: string;
  format?: string;
  is_default?: boolean;
}

export interface Video {
  id: string;
  title: string;
  description?: string | null;
  duration_sec?: number | null;
  poster_url?: string | null;
}

export interface Chapter {
  start_sec: number;
  end_sec: number;
  title: string;
}

export interface QueueItem {
  pos: number;
  id: string;
  title?: string | null;
  filename?: string;
  duration_sec?: number | null;
  poster?: string;
  position_sec?: number;
  series_name?: string;
}

export interface Queue {
  id: string;
  current_pos: number;
  version: number;
  items: QueueItem[];
}

/** An answer of the home that was not a success. */
export class HomeError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string
  ) {
    super(message);
    this.name = "HomeError";
  }

  /** The grant is over or does not reach this: a new cast is needed. */
  get grantEnded(): boolean {
    return this.status === 401 || this.code === "cast_scope";
  }
}

export interface OpenSession {
  video_id: string;
  start_sec?: number;
  device_caps?: DeviceCaps;
  force_transcode?: boolean;
}

export class HomeClient {
  constructor(
    readonly base: string,
    private grant: string,
    private readonly fetchFn: Fetch = (i, init) => fetch(i, init)
  ) {}

  /** A grant handed over later by the sender (the custom namespace's `grant` message). */
  setGrant(grant: string): void {
    this.grant = grant;
  }

  private async call<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.grant}`,
      "X-Maktaba-Client": "cast-receiver",
    };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await this.fetchFn(`${this.base}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
    });
    if (!res.ok) {
      let code = "";
      let detail = "";
      try {
        const p = (await res.json()) as { code?: string; detail?: string };
        code = p.code ?? "";
        detail = p.detail ?? "";
      } catch {
        // not a problem document
      }
      throw new HomeError(
        res.status,
        code,
        `${method} ${path}: ${res.status}${detail ? ` (${detail})` : ""}`
      );
    }
    if (res.status === 204) return undefined as T;
    return (await res.json()) as T;
  }

  /**
   * A stream session for a video, profile `cast`, with the device's report. A home older than
   * story 58.02 refuses the unknown `device_caps` field (400): the session is asked again
   * without it, on the profile alone.
   */
  async openSession(req: OpenSession): Promise<Session> {
    const body: Record<string, unknown> = { video_id: req.video_id, client_profile: "cast" };
    if (req.start_sec && req.start_sec > 0) body.start_sec = req.start_sec;
    if (req.force_transcode) body.force_transcode = true;
    if (req.device_caps) {
      try {
        return await this.call<Session>("POST", "/api/stream/sessions", {
          ...body,
          device_caps: req.device_caps,
        });
      } catch (e) {
        if (!(e instanceof HomeError) || e.status !== 400) throw e;
      }
    }
    return this.call<Session>("POST", "/api/stream/sessions", body);
  }

  progress(sessionId: string, positionSec: number, completed?: boolean): Promise<unknown> {
    const body: Record<string, unknown> = { position_sec: Math.max(0, positionSec) };
    if (completed) body.completed = true;
    return this.call(
      "POST",
      `/api/stream/sessions/${encodeURIComponent(sessionId)}/progress`,
      body
    );
  }

  closeSession(sessionId: string): Promise<unknown> {
    return this.call("DELETE", `/api/stream/sessions/${encodeURIComponent(sessionId)}`);
  }

  video(id: string): Promise<Video> {
    return this.call<Video>("GET", `/api/videos/${encodeURIComponent(id)}`);
  }

  chapters(id: string): Promise<Chapter[]> {
    return this.call<Chapter[]>("GET", `/api/videos/${encodeURIComponent(id)}/chapters`);
  }

  async subtitles(id: string): Promise<SubtitleTrack[]> {
    const out = await this.call<{ items?: SubtitleTrack[] } | SubtitleTrack[]>(
      "GET",
      `/api/videos/${encodeURIComponent(id)}/subtitles`
    );
    return Array.isArray(out) ? out : (out?.items ?? []);
  }

  queue(id: string): Promise<Queue> {
    return this.call<Queue>("GET", `/api/me/play-queues/${encodeURIComponent(id)}`);
  }

  setCurrent(id: string, pos: number, version: number): Promise<Queue> {
    return this.call<Queue>("PUT", `/api/me/play-queues/${encodeURIComponent(id)}/current`, {
      pos,
      version,
    });
  }

  /** The poster as an object URL for an <img>: the route takes the bearer, which an <img>
   * cannot send. "" when there is none. */
  async poster(id: string): Promise<string> {
    try {
      const res = await this.fetchFn(`${this.base}/api/videos/${encodeURIComponent(id)}/poster`, {
        headers: { Authorization: `Bearer ${this.grant}`, "X-Maktaba-Client": "cast-receiver" },
      });
      if (!res.ok) return "";
      return URL.createObjectURL(await res.blob());
    } catch {
      return "";
    }
  }
}

/**
 * The sidecar subtitle tracks of a direct-play session: one WebVTT URL per language, on the
 * stream's own origin, signed with the session's `stream_sig` (story 58.02's
 * `/stream/{session_id}/subs/sidecar/{lang}.vtt?sig=`). None without the token (a home older
 * than 58.02) or without a direct URL. An HLS session carries its WebVTT renditions in the
 * manifest (story 48.4) instead.
 */
export function sidecarTracks(
  session: Session,
  subs: readonly SubtitleTrack[]
): { language: string; url: string; isDefault: boolean }[] {
  if (!session.stream_sig || !session.direct_url) return [];
  let origin: string;
  try {
    origin = new URL(session.direct_url).origin;
  } catch {
    return [];
  }
  const seen = new Set<string>();
  const out: { language: string; url: string; isDefault: boolean }[] = [];
  for (const s of subs) {
    const lang = (s.language || "").toLowerCase();
    if (!/^[a-z]{2,3}(-[a-z0-9]{2,8})?$/.test(lang) || seen.has(lang)) continue;
    seen.add(lang);
    out.push({
      language: lang,
      url: `${origin}/stream/${encodeURIComponent(session.session_id)}/subs/sidecar/${lang}.vtt?sig=${encodeURIComponent(session.stream_sig)}`,
      isDefault: s.is_default === true,
    });
  }
  return out;
}

/** When a grant ends (its `exp`), read from the token without checking it: the receiver only
 * plans around it, and the home checks every call. 0 when it cannot be read. */
export function grantExpiry(grant: string): number {
  const token = grant.startsWith("cast_") ? grant.slice(5) : grant;
  const part = token.split(".")[1];
  if (!part) return 0;
  try {
    const json = atob(
      part
        .replace(/-/g, "+")
        .replace(/_/g, "/")
        .padEnd(Math.ceil(part.length / 4) * 4, "=")
    );
    const exp = (JSON.parse(json) as { exp?: unknown }).exp;
    return typeof exp === "number" ? exp * 1000 : 0;
  } catch {
    return 0;
  }
}
