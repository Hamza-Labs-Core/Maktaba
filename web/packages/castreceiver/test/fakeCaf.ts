// A fake of the CAF surface the receiver uses (src/receiver/caf.ts): a player manager whose
// events the test fires, a device that answers canDisplayType from a list, and the messages
// sent on the custom namespace. And a fake home: routes answered from a table, every call
// recorded.

import type {
  CafContext,
  CafFramework,
  CafLoadRequest,
  CafPlayerManager,
} from "../src/receiver/caf";

type Listener = (event: { type: string; [k: string]: unknown }) => void;

export class FakePlayer implements CafPlayerManager {
  interceptor: ((req: CafLoadRequest) => unknown) | null = null;
  listeners = new Map<string, Listener[]>();
  time = 0;
  duration = 0;
  loads: CafLoadRequest[] = [];
  /** What the interceptor answered for each load the receiver itself started. */
  answers: unknown[] = [];

  setMessageInterceptor(_type: string, fn: ((req: CafLoadRequest) => unknown) | null): void {
    this.interceptor = fn;
  }
  addEventListener(type: string | string[], fn: Listener): void {
    for (const t of Array.isArray(type) ? type : [type])
      this.listeners.set(t, [...(this.listeners.get(t) ?? []), fn]);
  }
  emit(type: string, extra: Record<string, unknown> = {}): void {
    for (const fn of this.listeners.get(type) ?? []) fn({ type, ...extra });
  }
  getCurrentTimeSec(): number {
    return this.time;
  }
  getDurationSec(): number {
    return this.duration;
  }
  async load(req: CafLoadRequest): Promise<void> {
    this.loads.push(req);
    if (this.interceptor) this.answers.push(await this.interceptor(req));
  }
}

export interface FakeDevice {
  /** codecs strings canDisplayType answers yes for, with the tallest height each plays */
  video: Record<string, number>;
  audio: string[];
  /** codecs strings it also plays at 60 fps (anything else: 30 at most) */
  fps60?: string[];
  caps?: Record<string, unknown>;
}

export const CHROMECAST_GOOGLE_TV: FakeDevice = {
  video: {
    "avc1.640028": 1080,
    "avc1.4d401f": 720,
    "hev1.1.6.L153.B0": 2160,
    "hev1.2.4.L153.B0": 2160,
    "hev1.1.6.L120.B0": 1080,
    "hev1.2.4.L120.B0": 1080,
    "dvh1.05.06": 2160,
    "dvh1.08.06": 2160,
  },
  audio: ["mp4a.40.2", "ac-3", "ec-3"],
  fps60: ["avc1.640028", "hev1.1.6.L153.B0", "hev1.2.4.L153.B0"],
  caps: { is_hdr_supported: true, is_dv_supported: true, is_dolby_atmos_supported: true },
};

export const CHROMECAST_3RD_GEN: FakeDevice = {
  video: { "avc1.640028": 1080, "avc1.4d401f": 720 },
  audio: ["mp4a.40.2"],
  caps: {},
};

export class FakeContext implements CafContext {
  player = new FakePlayer();
  sent: { namespace: string; senderId?: string; data: unknown }[] = [];
  listeners = new Map<string, (e: { senderId: string; data: unknown }) => void>();
  started: Record<string, unknown> | undefined;
  constructor(private readonly device: FakeDevice = CHROMECAST_GOOGLE_TV) {}
  getPlayerManager(): FakePlayer {
    return this.player;
  }
  start(options?: Record<string, unknown>): void {
    this.started = options;
  }
  canDisplayType(
    mime: string,
    codecs?: string,
    _w?: number,
    height?: number,
    fps?: number
  ): boolean {
    if (mime.startsWith("audio/")) return this.device.audio.includes(codecs ?? "");
    const max = this.device.video[codecs ?? ""];
    if ((fps ?? 0) > 30 && !(this.device.fps60 ?? []).includes(codecs ?? "")) return false;
    return max !== undefined && (height ?? 0) <= max;
  }
  getDeviceCapabilities(): Record<string, unknown> {
    return this.device.caps ?? {};
  }
  addCustomMessageListener(
    namespace: string,
    fn: (e: { senderId: string; data: unknown }) => void
  ): void {
    this.listeners.set(namespace, fn);
  }
  sendCustomMessage(namespace: string, senderId: string | undefined, data: unknown): void {
    this.sent.push({ namespace, senderId, data });
  }
}

export function fakeFramework(ctx: FakeContext): CafFramework {
  class ErrorData {
    reason?: string;
    customData?: unknown;
    constructor(readonly type: string) {}
  }
  return {
    CastReceiverContext: { getInstance: () => ctx },
    messages: {
      MessageType: { LOAD: "LOAD" },
      HlsSegmentFormat: { TS: "ts", FMP4: "fmp4" },
      HlsVideoSegmentFormat: { MPEG2_TS: "mpeg2_ts", FMP4: "fmp4" },
      StreamType: { BUFFERED: "BUFFERED" },
      ErrorType: { LOAD_FAILED: "LOAD_FAILED" },
      ErrorData,
    },
    events: {
      EventType: {
        PLAYING: "PLAYING",
        PAUSE: "PAUSE",
        BUFFERING: "BUFFERING",
        SEEKED: "SEEKED",
        TIME_UPDATE: "TIME_UPDATE",
        MEDIA_FINISHED: "MEDIA_FINISHED",
        ERROR: "ERROR",
      },
    },
    system: {
      DeviceCapabilities: {
        IS_HDR_SUPPORTED: "is_hdr_supported",
        IS_DV_SUPPORTED: "is_dv_supported",
        IS_DOLBY_ATMOS_SUPPORTED: "is_dolby_atmos_supported",
      },
    },
  };
}

export interface Call {
  method: string;
  url: string;
  headers: Record<string, string>;
  body: unknown;
}

type Route = (call: Call) => { status?: number; body?: unknown; hang?: boolean } | undefined;

/** A fake fetch over a table of "METHOD url-prefix" → answer. An unknown route is a 404; a
 * `hang` answer never resolves (until aborted), as a LAN address the TV cannot reach. */
export function fakeHome(
  routes: Record<string, Route | { status?: number; body?: unknown; hang?: boolean }>
) {
  const calls: Call[] = [];
  const fetchFn = (input: string, init: RequestInit = {}): Promise<Response> => {
    const method = (init.method ?? "GET").toUpperCase();
    const headers = Object.fromEntries(
      Object.entries((init.headers as Record<string, string>) ?? {})
    );
    const body = typeof init.body === "string" ? JSON.parse(init.body) : undefined;
    const call: Call = { method, url: input, headers, body };
    calls.push(call);
    const key = Object.keys(routes)
      .filter((k) => {
        const [m, prefix] = k.split(" ");
        return m === method && input.startsWith(prefix);
      })
      .sort((a, b) => b.length - a.length)[0];
    const r = key ? routes[key] : undefined;
    const answer = typeof r === "function" ? r(call) : r;
    if (answer?.hang) {
      return new Promise((_, reject) => {
        init.signal?.addEventListener("abort", () =>
          reject(new DOMException("aborted", "AbortError"))
        );
      });
    }
    const status = answer?.status ?? (answer ? 200 : 404);
    return Promise.resolve(
      new Response(answer?.body === undefined ? null : JSON.stringify(answer.body), {
        status,
        headers: { "Content-Type": "application/json" },
      })
    );
  };
  return { fetch: fetchFn, calls };
}
