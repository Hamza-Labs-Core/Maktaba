// React binding of the TV player (Epic 34.6): one hook owns the switcher,
// the two video elements, number entry, gestures and neighbour prefetch;
// the host page renders the stage and its overlays from what it returns.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { attachGestures, type Gesture } from "./gestures";
import { fromTextField, keyToAction, type TvAction } from "./keymap";
import type { LiveMode } from "./live";
import { NumberEntry, NUMBER_COMMIT_MS } from "./numberEntry";
import { VideoPipeline } from "./pipeline";
import { Prefetcher } from "./prefetch";
import { ChannelRing, type RingChannel } from "./ring";
import { Switcher, type Resolver, type SwitchEvent, type SwitcherState } from "./switcher";

export interface TvSwitcherOptions<C extends RingChannel> {
  /** The channels the viewer may watch (null while loading). */
  channels: C[] | null;
  resolve: Resolver<C>;
  /** The channel to play first (deep link); changing it tunes it. */
  channel: C | null;
  /** A channel settled: update the URL, etc. */
  onSettled?: (c: C, from: C | null) => void;
  /** Playlists to prefetch for the neighbours of a settled channel (prev, next, last). */
  prefetchUrls?: (neighbours: C[]) => string[];
  /** Rewrites prefetch URLs (the remote token). */
  prefetchRewrite?: (url: string) => string;
  telemetry?: (e: SwitchEvent) => void;
  isWarm?: (c: C) => boolean;
  /** Actions the host handles itself (guide, info, escape, enter without digits). */
  onAction?: (a: TvAction) => void;
  /** The visible stream died for good (the host may offer another path). */
  onFatal?: () => void;
  /** Re-creates the switcher (a path change: LAN ↔ remote). */
  pathKey?: string;
}

export interface TvSwitcher<C extends RingChannel> {
  state: SwitcherState<C>;
  /** Callback refs for the two <video> elements. */
  videoRef: [(el: HTMLVideoElement | null) => void, (el: HTMLVideoElement | null) => void];
  activeIndex: number;
  mode: LiveMode;
  reconnecting: boolean;
  mutedByPolicy: boolean;
  /** Callback ref for the element that takes swipes and taps. */
  gestureRef: (el: HTMLElement | null) => void;
  numberBuffer: string;
  /** "No channel N" (EC2) for 1.5 s. */
  noChannel: number | null;
  surf: (dir: number) => void;
  tune: (c: C) => void;
  last: () => void;
  retune: () => void;
  onKeyDown: (e: React.KeyboardEvent | KeyboardEvent) => void;
  /** The active <video> element. */
  activeVideo: () => HTMLVideoElement | null;
}

const IDLE = { current: null, target: null, phase: "idle", status: null, error: null, last: null };

export function useTvSwitcher<C extends RingChannel>(o: TvSwitcherOptions<C>): TvSwitcher<C> {
  const [els, setEls] = useState<[HTMLVideoElement | null, HTMLVideoElement | null]>([null, null]);
  const [state, setState] = useState<SwitcherState<C>>(IDLE as SwitcherState<C>);
  const [activeIndex, setActive] = useState(0);
  const [mode, setMode] = useState<LiveMode>("idle");
  const [reconnecting, setReconnecting] = useState(false);
  const [mutedByPolicy, setMutedByPolicy] = useState(false);
  const [numberBuffer, setNumberBuffer] = useState("");
  const [noChannel, setNoChannel] = useState<number | null>(null);
  const [ready, setReady] = useState(false);
  const opts = useRef(o);
  opts.current = o;

  const ring = useMemo(() => new ChannelRing(o.channels ?? []), [o.channels]);
  const ringRef = useRef(ring);
  ringRef.current = ring;
  const switcherRef = useRef<Switcher<C> | null>(null);
  // the channel last reported settled: the host's URL follows it, so a deep
  // link equal to it is our own echo, not a request to tune
  const settledRef = useRef<string | null>(null);
  const pipelineRef = useRef<VideoPipeline | null>(null);
  const prefetchRef = useRef<Prefetcher | null>(null);

  const refA = useCallback(
    (el: HTMLVideoElement | null) => setEls((p) => (p[0] === el ? p : [el, p[1]])),
    []
  );
  const refB = useCallback(
    (el: HTMLVideoElement | null) => setEls((p) => (p[1] === el ? p : [p[0], el])),
    []
  );

  // the switcher lives while both elements are mounted (and per path)
  useEffect(() => {
    const [a, b] = els;
    if (!a || !b) return;
    const prefetcher = new Prefetcher({ rewrite: (u) => opts.current.prefetchRewrite?.(u) ?? u });
    prefetchRef.current = prefetcher;
    const pipeline = new VideoPipeline([a, b], {
      onActive: setActive,
      onMode: setMode,
      onReconnecting: setReconnecting,
      onMutedByPolicy: () => setMutedByPolicy(true),
      onFatal: () => {
        setReconnecting(false);
        opts.current.onFatal?.();
      },
    });
    pipelineRef.current = pipeline;
    const sw = new Switcher<C>({
      ring: ringRef.current,
      resolve: (c, ctx) => opts.current.resolve(c, ctx),
      pipeline,
      onState: setState,
      telemetry: (e) => opts.current.telemetry?.(e),
      isWarm: (c) => opts.current.isWarm?.(c) ?? false,
      isPrefetched: (c) => {
        const urls = opts.current.prefetchUrls?.([c]) ?? [];
        return urls.some((u) => prefetcher.has(u));
      },
      onSettled: (c, from) => {
        settledRef.current = c.id;
        const r = ringRef.current;
        const ns = r.neighbours(c.id);
        const last = from && from.id !== c.id ? r.byId(from.id) : null;
        if (last && !ns.some((x) => x.id === last.id)) ns.push(last);
        prefetcher.set(opts.current.prefetchUrls?.(ns) ?? []);
        opts.current.onSettled?.(c, from);
      },
    });
    switcherRef.current = sw;
    setState(sw.state);
    setReady(true);
    return () => {
      setReady(false);
      settledRef.current = null;
      sw.dispose();
      pipeline.destroy();
      prefetcher.stop();
      switcherRef.current = null;
      pipelineRef.current = null;
      prefetchRef.current = null;
      setState(IDLE as SwitcherState<C>);
    };
  }, [els, o.pathKey]);

  useEffect(() => {
    switcherRef.current?.setRing(ring);
  }, [ring]);

  // the deep-linked channel: tune it when it differs from what plays or is carded
  const want = o.channel;
  useEffect(() => {
    const sw = switcherRef.current;
    if (!sw || !want || want.id === settledRef.current) return;
    const s = sw.state;
    if (s.target?.id === want.id || (s.current?.id === want.id && !s.target)) return;
    sw.tune(want);
  }, [want, ready]);

  // number entry → a channel, or "No channel N" (EC2)
  const entryRef = useRef<NumberEntry | null>(null);
  const noTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  if (!entryRef.current) {
    entryRef.current = new NumberEntry(setNumberBuffer, (n) => {
      const c = ringRef.current.byNumber(n);
      if (c) {
        switcherRef.current?.tune(c);
        return;
      }
      setNoChannel(n);
      if (noTimer.current) clearTimeout(noTimer.current);
      noTimer.current = setTimeout(() => setNoChannel(null), NUMBER_COMMIT_MS);
    });
  }
  useEffect(
    () => () => {
      entryRef.current?.cancel();
      if (noTimer.current) clearTimeout(noTimer.current);
    },
    []
  );

  const surf = useCallback((dir: number) => switcherRef.current?.surf(dir), []);
  const tune = useCallback((c: C) => switcherRef.current?.tune(c), []);
  const last = useCallback(() => switcherRef.current?.lastChannel(), []);
  const retune = useCallback(() => switcherRef.current?.retune(), []);

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent | KeyboardEvent) => {
      if (fromTextField(e.target)) return;
      const a = keyToAction(e);
      if (!a) return;
      // the native controls keep their own keys while focused
      const tgt = e.target as HTMLElement | null;
      if (tgt?.tagName === "BUTTON" && a.type === "enter") return;
      e.preventDefault();
      switch (a.type) {
        case "next":
          surf(1);
          return;
        case "prev":
          surf(-1);
          return;
        case "digit":
          entryRef.current?.digit(a.digit);
          return;
        case "enter":
          if (!entryRef.current?.commit()) opts.current.onAction?.(a);
          return;
        case "last":
          last();
          return;
        case "escape":
          entryRef.current?.cancel();
          opts.current.onAction?.(a);
          return;
        default:
          opts.current.onAction?.(a);
      }
    },
    [surf, last]
  );

  // swipes on the video: up = next, down = previous; a tap opens the mini-guide
  const detachGestures = useRef<(() => void) | null>(null);
  const gestureRef = useCallback(
    (el: HTMLElement | null) => {
      detachGestures.current?.();
      detachGestures.current = null;
      if (!el) return;
      detachGestures.current = attachGestures(el, (g: Gesture) => {
        if (g === "swipe_up") surf(1);
        else if (g === "swipe_down") surf(-1);
        else opts.current.onAction?.({ type: "info" });
      });
    },
    [surf]
  );

  return {
    state,
    videoRef: [refA, refB],
    activeIndex,
    mode,
    reconnecting,
    mutedByPolicy,
    gestureRef,
    numberBuffer,
    noChannel,
    surf,
    tune,
    last,
    retune,
    onKeyDown,
    activeVideo: () => pipelineRef.current?.activeElement ?? null,
  };
}
