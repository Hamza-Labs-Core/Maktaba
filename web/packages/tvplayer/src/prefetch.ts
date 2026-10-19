// Neighbour playlists (Epic 34.6 AC5, design §8.3): after a channel settles
// the player fetches the playlists of the previous, next and last channel,
// and refreshes them every 10 s while the tab is visible, so a switch finds
// the live edge known and the connection warm. Playlists only, never a
// segment: segments are what Cloudflare bills (and on the LAN, the host app
// only offers channels already on air, so no encoder is started for them).

export const PREFETCH_EVERY_MS = 10_000;

export type FetchLike = (url: string, init?: RequestInit) => Promise<Response>;

/** Whether a URL is a playlist (the only thing the prefetcher requests). */
export function isPlaylist(url: string): boolean {
  const path = url.split(/[?#]/, 1)[0];
  return path.endsWith(".m3u8");
}

/** The first media playlist of a master playlist's text, resolved against base. */
export function firstVariant(master: string, base: string): string | null {
  if (!master.includes("#EXT-X-STREAM-INF")) return null;
  const lines = master.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].startsWith("#EXT-X-STREAM-INF")) continue;
    for (let j = i + 1; j < lines.length; j++) {
      const l = lines[j].trim();
      if (!l || l.startsWith("#")) continue;
      try {
        return new URL(l, new URL(base, "http://x/")).toString().replace(/^http:\/\/x/, "");
      } catch {
        return null;
      }
    }
  }
  return null;
}

export interface PrefetcherOptions {
  fetch?: FetchLike;
  every?: number;
  /** Whether the page is visible (refreshes pause in a background tab, EC4). */
  visible?: () => boolean;
  /** Rewrites each URL before it is fetched (the remote token swap). */
  rewrite?: (url: string) => string;
}

export class Prefetcher {
  private urls: string[] = [];
  private timer: ReturnType<typeof setInterval> | null = null;
  private readonly fetchFn: FetchLike;
  private readonly every: number;
  private readonly visible: () => boolean;
  private onVis: (() => void) | null = null;
  /** Every URL requested (tests and telemetry). */
  readonly requested: string[] = [];

  constructor(private readonly opts: PrefetcherOptions = {}) {
    this.fetchFn = opts.fetch ?? ((u, i) => fetch(u, i));
    this.every = opts.every ?? PREFETCH_EVERY_MS;
    this.visible =
      opts.visible ??
      (() => typeof document === "undefined" || document.visibilityState !== "hidden");
  }

  /** Whether a URL is one of the prefetched playlists. */
  has(url: string): boolean {
    return this.urls.includes(url);
  }

  /** Prefetch these playlists now and every `every` ms (replacing the last set). */
  set(urls: string[]): void {
    this.urls = urls.filter(isPlaylist);
    this.stopTimer();
    if (this.urls.length === 0) return;
    void this.round();
    this.timer = setInterval(() => {
      if (this.visible()) void this.round();
    }, this.every);
    if (!this.onVis && typeof document !== "undefined") {
      this.onVis = () => {
        if (this.visible() && this.urls.length) void this.round();
      };
      document.addEventListener("visibilitychange", this.onVis);
    }
  }

  stop(): void {
    this.urls = [];
    this.stopTimer();
    if (this.onVis && typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.onVis);
    }
    this.onVis = null;
  }

  private stopTimer() {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  }

  private async get(url: string): Promise<string | null> {
    if (!isPlaylist(url)) return null; // never a segment
    const u = this.opts.rewrite ? this.opts.rewrite(url) : url;
    this.requested.push(u);
    try {
      const res = await this.fetchFn(u, { credentials: "include", cache: "no-store" });
      if (!res.ok) return null;
      return await res.text();
    } catch {
      return null;
    }
  }

  private async round() {
    await Promise.all(
      this.urls.map(async (url) => {
        const text = await this.get(url);
        const variant = text ? firstVariant(text, url) : null;
        if (variant && isPlaylist(variant)) await this.get(variant);
      })
    );
  }
}
