// Two <video> elements, make before break (Epic 34.6 D2, design §8.3): the
// visible element keeps playing the old channel while the hidden one loads
// the new channel (muted, at the live edge); once it has buffered its first
// segment and fired canplay they swap and the old one is stopped.
import { attachLive, type LiveHandle, type LiveMode, type LiveOptions } from "./live";
import { SwitchError, type Pipeline, type Stream } from "./switcher";

export const PREPARE_TIMEOUT_MS = 30_000;

export interface VideoPipelineOptions {
  /** The visible element changed (0 or 1). */
  onActive?: (index: number) => void;
  /** The visible stream is reconnecting (stall rejoin). */
  onReconnecting?: (on: boolean) => void;
  /** The visible stream died and cannot be recovered. */
  onFatal?: () => void;
  /** The visible element's mode (native / hls / unsupported). */
  onMode?: (mode: LiveMode) => void;
  /** The browser refused sound without a tap: the picture plays muted. */
  onMutedByPolicy?: (muted: boolean) => void;
  loadHls?: LiveOptions["loadHls"];
  timeoutMs?: number;
}

function reset(el: HTMLVideoElement) {
  try {
    if (!el.paused) el.pause();
  } catch {
    /* not a real media element */
  }
  if (el.hasAttribute("src")) {
    el.removeAttribute("src");
    try {
      el.load();
    } catch {
      /* not a real media element */
    }
  }
}

export class VideoPipeline implements Pipeline {
  private active = 0;
  private handles: [LiveHandle | null, LiveHandle | null] = [null, null];
  private modes: [LiveMode, LiveMode] = ["idle", "idle"];
  private pending: (() => void) | null = null;
  /** The element prepare is loading (the visible one when nothing plays yet). */
  private preparing: number | null = null;
  private wantMuted = false;
  /** The browser, not the viewer, muted the picture: not carried to the next channel. */
  private policyMuted = false;
  private volume = 1;

  constructor(
    private readonly els: [HTMLVideoElement, HTMLVideoElement],
    private readonly o: VideoPipelineOptions = {}
  ) {}

  get activeIndex(): number {
    return this.active;
  }

  get activeElement(): HTMLVideoElement {
    return this.els[this.active];
  }

  activeNative(): boolean {
    return this.modes[this.active] === "native";
  }

  prepare(stream: Stream, signal: AbortSignal): Promise<void> {
    this.discard();
    // nothing plays yet: load straight on the visible element
    const idx = this.handles[this.active] ? 1 - this.active : this.active;
    this.preparing = idx;
    const el = this.els[idx];
    const cur = this.els[this.active];
    if (this.handles[this.active]) {
      // a mute the browser forced is not the viewer's choice: the next channel asks
      // for sound again (switching is itself a tap, so it is usually allowed now)
      if (!this.policyMuted) this.wantMuted = cur.muted;
      this.volume = cur.volume;
    }
    el.muted = true; // a hidden element never makes a sound (and may autoplay)
    el.preload = "auto";
    return new Promise<void>((resolve, reject) => {
      let done = false;
      let timer: ReturnType<typeof setTimeout> | null = null;
      const finish = (err?: unknown) => {
        if (done) return;
        done = true;
        if (timer) clearTimeout(timer);
        el.removeEventListener("canplay", onReady);
        el.removeEventListener("loadeddata", onReady);
        el.removeEventListener("playing", onPlaying);
        el.removeEventListener("error", onError);
        signal.removeEventListener("abort", onAbort);
        this.pending = null;
        if (err) {
          this.dropStandby(idx);
          if (this.preparing === idx) this.preparing = null;
          reject(err);
        } else {
          resolve();
        }
      };
      const onReady = () => {
        // first segment buffered and playable (canplay alone can precede data)
        if (el.readyState >= 2 || (el.buffered && el.buffered.length > 0)) finish();
      };
      const onPlaying = () => finish(); // frames are flowing
      const onError = () => {
        if (this.modes[idx] !== "hls") finish(new SwitchError("unavailable", undefined, true));
      };
      const onAbort = () => finish(new SwitchError("aborted"));
      this.pending = () => finish(new SwitchError("aborted"));
      el.addEventListener("canplay", onReady);
      el.addEventListener("loadeddata", onReady);
      el.addEventListener("playing", onPlaying);
      el.addEventListener("error", onError);
      signal.addEventListener("abort", onAbort);
      timer = setTimeout(
        () => finish(new SwitchError("timeout", undefined, true)),
        this.o.timeoutMs ?? PREPARE_TIMEOUT_MS
      );
      this.handles[idx] = attachLive(el, stream.url, {
        rewrite: stream.rewrite,
        loadHls: this.o.loadHls,
        onMode: (m) => {
          this.modes[idx] = m;
          if (m === "unsupported") finish(new SwitchError("unsupported"));
          if (idx === this.active) this.o.onMode?.(m);
        },
        onReconnecting: (on) => {
          if (idx === this.active) this.o.onReconnecting?.(on);
        },
        onFatal: () => {
          if (!done) finish(new SwitchError("unavailable", undefined, true));
          else if (idx === this.active) this.o.onFatal?.();
        },
      });
      // muted autoplay is always allowed; it also makes iOS fetch the stream
      void el.play?.()?.catch?.(() => {});
    });
  }

  promote(): void {
    const next = this.preparing ?? 1 - this.active;
    this.preparing = null;
    const old = this.active;
    const el = this.els[next];
    this.active = next;
    el.volume = this.volume;
    el.muted = this.wantMuted;
    if (this.policyMuted) this.o.onMutedByPolicy?.(false);
    this.policyMuted = false;
    const p = el.play?.();
    if (p && typeof p.catch === "function") {
      p.catch(() => {
        // sound needs a tap: keep the picture, muted
        el.muted = true;
        this.policyMuted = true;
        void el.play?.()?.catch?.(() => {});
        this.o.onMutedByPolicy?.(true);
      });
    }
    if (old !== next) this.dropStandby(old);
    this.o.onActive?.(next);
    this.o.onMode?.(this.modes[next]);
  }

  discard(): void {
    if (this.pending) this.pending();
    const idx = this.preparing ?? 1 - this.active;
    this.preparing = null;
    this.dropStandby(idx);
  }

  /** Stops both elements (unmount). */
  destroy(): void {
    this.discard();
    this.dropStandby(this.active);
  }

  private dropStandby(idx: number) {
    this.handles[idx]?.detach();
    this.handles[idx] = null;
    this.modes[idx] = "idle";
    reset(this.els[idx]);
  }
}
