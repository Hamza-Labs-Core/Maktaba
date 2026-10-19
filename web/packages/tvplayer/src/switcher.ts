// The channel switcher (Epic 34.6, design §8.3): a framework-free state
// machine over a channel ring, a host-supplied resolver and a two-element
// pipeline.
//
//   input → target moves at once (the tuning card) → [surf: 300 ms debounce]
//         → resolve(target) → standby element loads it → first segment and
//           canplay → swap (make before break) → the old stream is released
//
// At most one tune is in flight: a newer commit aborts the older one (its
// stream, if it arrived, is released unplayed). A failed target shows its
// reason on the card; after 3 s the card returns to the channel still
// playing unless the viewer moved on (EC1).
import { ChannelRing, type RingChannel } from "./ring";

export const SURF_COMMIT_MS = 300;
export const ERROR_RETURN_MS = 3000;
export const PREPARE_RETRIES = 3;
export const PREPARE_RETRY_MS = 2000;

/** A playable stream from the host app's resolver. */
export interface Stream {
  url: string;
  /** Maps each playlist/segment URL before it is requested (remote token swap). */
  rewrite?: (url: string) => string;
  /** The stream is no longer needed (ended remote session, …). Called once. */
  release(): void;
  /** Native HLS cannot rewrite URLs: the stream reports a new URL to swap to. */
  onUrl?: (cb: (url: string) => void) => () => void;
  /** "lan" | "remote" (telemetry). */
  path?: string;
}

export interface ResolveContext {
  signal: AbortSignal;
  /** 0 for the first try; the LAN resolver adds a cache-buster on retries. */
  attempt: number;
  /** A status for the card ("starting" while a remote push starts). */
  status(key: string | null): void;
  /** Ends the playing stream now (a remote per-user limit) — break before make. */
  freeSlot(): void;
}

export type Resolver<C> = (channel: C, ctx: ResolveContext) => Promise<Stream>;

/** Why a target could not be tuned (shown on the card). */
export class SwitchError extends Error {
  constructor(
    readonly code: string,
    message?: string,
    readonly retryable = false
  ) {
    super(message ?? code);
    this.name = "SwitchError";
  }
}

export interface Pipeline {
  /** Loads the stream on the hidden element; resolves at its first segment + canplay. */
  prepare(stream: Stream, signal: AbortSignal): Promise<void>;
  /** The hidden element becomes the visible one; the old one stops. */
  promote(): void;
  /** Drops what prepare loaded. */
  discard(): void;
  /** The visible element plays natively (no URL rewriting). */
  activeNative(): boolean;
}

export type Phase = "idle" | "pending" | "loading" | "error";

export interface SwitcherState<C> {
  /** The channel playing. */
  current: C | null;
  /** The channel the card shows while switching (null: none). */
  target: C | null;
  phase: Phase;
  /** The resolver's status for the target ("starting", …). */
  status: string | null;
  error: SwitchError | null;
  /** The channel before the current one ("last channel"). */
  last: C | null;
}

export interface SwitchEvent {
  from: string | null;
  to: string;
  ms_to_card: number;
  ms_to_first_frame: number;
  path: "warm" | "cold";
  prefetched: boolean;
  via: string;
}

export interface SwitcherOptions<C extends RingChannel> {
  ring: ChannelRing<C>;
  resolve: Resolver<C>;
  pipeline: Pipeline;
  onState: (s: SwitcherState<C>) => void;
  /** A channel settled (playing): deep link, prefetch of neighbours. */
  onSettled?: (c: C, from: C | null) => void;
  telemetry?: (e: SwitchEvent) => void;
  /** Whether a channel is warm (on air) and whether its playlist was prefetched. */
  isWarm?: (c: C) => boolean;
  isPrefetched?: (c: C) => boolean;
  now?: () => number;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (h: unknown) => void;
  surfMs?: number;
  retries?: number;
  retryMs?: number;
}

export class Switcher<C extends RingChannel> {
  private st: SwitcherState<C> = {
    current: null,
    target: null,
    phase: "idle",
    status: null,
    error: null,
    last: null,
  };
  private ring: ChannelRing<C>;
  private stream: Stream | null = null;
  private unsubUrl: (() => void) | null = null;
  private inflight: AbortController | null = null;
  private surfTimer: unknown = null;
  private errTimer: unknown = null;
  private inputAt = 0;
  private cardAt = 0;
  private disposed = false;
  private readonly now: () => number;
  private readonly setTimer: (fn: () => void, ms: number) => unknown;
  private readonly clearTimer: (h: unknown) => void;

  constructor(private readonly o: SwitcherOptions<C>) {
    this.ring = o.ring;
    this.now =
      o.now ?? (() => (typeof performance !== "undefined" ? performance.now() : Date.now()));
    this.setTimer = o.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = o.clearTimer ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>));
  }

  get state(): SwitcherState<C> {
    return this.st;
  }

  /** A new lineup (the order or the channels changed). */
  setRing(ring: ChannelRing<C>): void {
    this.ring = ring;
  }

  /** Channel up (+1) / down (−1): the card moves now, the tune follows 300 ms after the last press. */
  surf(dir: number): void {
    if (this.disposed) return;
    const base = this.st.target ?? this.st.current;
    const next = this.ring.step(base?.id, dir);
    if (!next) return;
    this.mark();
    this.cancelErr();
    this.set({ target: next, phase: "pending", status: null, error: null });
    this.clearSurf();
    this.surfTimer = this.setTimer(() => {
      this.surfTimer = null;
      void this.commit(next);
    }, this.o.surfMs ?? SURF_COMMIT_MS);
  }

  /** Tune a channel now (number entry, mini-guide, deep link). */
  tune(c: C): void {
    if (this.disposed) return;
    this.mark();
    this.cancelErr();
    this.clearSurf();
    this.set({ target: c, phase: "pending", status: null, error: null });
    void this.commit(c);
  }

  /** The last channel (L / Backspace / ↺). */
  lastChannel(): void {
    if (this.st.last) this.tune(this.st.last);
  }

  /** Re-tune the current channel (a stream that died mid-play). */
  retune(): void {
    if (this.st.current) {
      const c = this.st.current;
      this.mark();
      this.set({ target: c, phase: "pending", status: null, error: null });
      void this.commit(c, true);
    }
  }

  dispose(): void {
    this.disposed = true;
    this.clearSurf();
    this.cancelErr();
    this.inflight?.abort();
    this.inflight = null;
    this.o.pipeline.discard();
    this.releaseStream();
  }

  private mark() {
    this.inputAt = this.now();
  }

  private set(p: Partial<SwitcherState<C>>) {
    this.st = { ...this.st, ...p };
    if (p.target !== undefined && p.target) this.cardAt = this.now();
    this.o.onState(this.st);
  }

  private clearSurf() {
    if (this.surfTimer !== null) this.clearTimer(this.surfTimer);
    this.surfTimer = null;
  }

  private cancelErr() {
    if (this.errTimer !== null) this.clearTimer(this.errTimer);
    this.errTimer = null;
  }

  private releaseStream() {
    this.unsubUrl?.();
    this.unsubUrl = null;
    const s = this.stream;
    this.stream = null;
    if (s) {
      try {
        s.release();
      } catch {
        /* the host's release failing never blocks a switch */
      }
    }
  }

  private async commit(c: C, force = false): Promise<void> {
    if (!force && this.st.current?.id === c.id && !this.inflight) {
      this.set({ target: null, phase: "idle", status: null, error: null });
      return;
    }
    this.inflight?.abort();
    this.o.pipeline.discard();
    const ac = new AbortController();
    this.inflight = ac;
    const from = this.st.current;
    const t0 = this.inputAt;
    const cardMs = Math.max(0, this.cardAt - t0);
    this.set({ phase: "loading", status: null, error: null });
    const retries = this.o.retries ?? PREPARE_RETRIES;
    let lastErr: unknown = null;
    for (let attempt = 0; attempt < retries; attempt++) {
      if (ac.signal.aborted) return;
      let stream: Stream | null = null;
      try {
        stream = await this.o.resolve(c, {
          signal: ac.signal,
          attempt,
          status: (key) => {
            if (!ac.signal.aborted) this.set({ status: key });
          },
          freeSlot: () => {
            if (!ac.signal.aborted) this.releaseStream();
          },
        });
        if (ac.signal.aborted) {
          stream.release();
          return;
        }
        await this.o.pipeline.prepare(stream, ac.signal);
        if (ac.signal.aborted) {
          stream.release();
          return;
        }
      } catch (e) {
        if (stream) {
          try {
            stream.release();
          } catch {
            /* ignore */
          }
        }
        if (ac.signal.aborted) return;
        lastErr = e;
        const retryable = !(e instanceof SwitchError) || e.retryable;
        if (!retryable || attempt + 1 >= retries) break;
        await this.sleep(this.o.retryMs ?? PREPARE_RETRY_MS, ac.signal);
        continue;
      }
      // the new channel can play: swap, then let the old one go
      this.o.pipeline.promote();
      this.releaseStream();
      this.stream = stream;
      this.watchUrl(stream);
      this.inflight = null;
      const last = from && from.id !== c.id ? from : this.st.last;
      this.set({ current: c, target: null, phase: "idle", status: null, error: null, last });
      this.o.telemetry?.({
        from: from?.id ?? null,
        to: c.id,
        ms_to_card: cardMs,
        ms_to_first_frame: Math.max(0, this.now() - t0),
        path: this.o.isWarm?.(c) ? "warm" : "cold",
        prefetched: this.o.isPrefetched?.(c) ?? false,
        via: stream.path ?? "",
      });
      this.o.onSettled?.(c, from);
      return;
    }
    if (ac.signal.aborted) return;
    this.inflight = null;
    const err =
      lastErr instanceof SwitchError
        ? lastErr
        : new SwitchError("unavailable", lastErr instanceof Error ? lastErr.message : undefined);
    this.set({ phase: "error", error: err, status: null });
    if (this.st.current && this.st.current.id !== c.id) {
      // EC1: back to the channel still playing unless the viewer moves on
      this.errTimer = this.setTimer(() => {
        this.errTimer = null;
        if (this.st.phase === "error" && this.st.target?.id === c.id) {
          this.set({ target: null, phase: "idle", error: null });
        }
      }, ERROR_RETURN_MS);
    }
  }

  // Safari: a new token comes as a new URL; swap it in on the hidden element.
  private watchUrl(stream: Stream) {
    if (!stream.onUrl) return;
    this.unsubUrl = stream.onUrl((url) => {
      if (this.disposed || this.stream !== stream || this.inflight) return;
      if (!this.o.pipeline.activeNative()) return; // hls.js rewrites the token itself
      const ac = new AbortController();
      this.inflight = ac;
      this.o.pipeline
        .prepare({ ...stream, url, release: () => {} }, ac.signal)
        .then(() => {
          if (!ac.signal.aborted) this.o.pipeline.promote();
        })
        .catch(() => this.o.pipeline.discard())
        .finally(() => {
          if (this.inflight === ac) this.inflight = null;
        });
    });
  }

  private sleep(ms: number, signal: AbortSignal): Promise<void> {
    return new Promise((resolve) => {
      const h = this.setTimer(resolve, ms);
      signal.addEventListener("abort", () => {
        this.clearTimer(h);
        resolve();
      });
    });
  }
}
