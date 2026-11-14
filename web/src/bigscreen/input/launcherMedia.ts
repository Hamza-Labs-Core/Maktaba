// Epic 58 (stories 58.10 and 58.11) — /tv/ plays through the Samsung or LG launcher. In a
// launcher whose hello lists `play`, the stream is the TV's own player (AVPlay, or webOS's
// native <video>) in the video plane under the iframe, and /tv/'s <video> is a stand-in:
// attachLauncher() gives that element the launcher's position, duration and state, and
// turns what the player and Watch do to it into bridge messages:
//
//   el.play() / el.pause()             maktaba:play (the first time) · resume · pause
//   el.currentTime = t                 maktaba:seek {sec}
//   el.audioTracks[i].enabled = true   maktaba:audioTrack {i}  (the state lists the tracks)
//   a text track's mode "showing"      maktaba:subtitle {id, subtitle_scale}  (the launcher
//                                      draws Maktaba's own subtitles over the video)
//   detach                             maktaba:stop
//
// and fires the element's own events (loadedmetadata, durationchange, timeupdate, play,
// pause, waiting, playing, seeking, seeked, ended) from the launcher's state, so the desk
// player's progress posts, stall reports and Watch's controls work unchanged. While it is
// attached, <html> carries `tv-launcher-video`: the page is transparent, so the video
// plane shows through.
//
// Sessions opened in a launcher say which TV they are for (client_profile tizen or webos)
// and what it decodes (the launcher's device_caps, story 58.02).

import {
  inLauncher,
  launcherHello,
  launcherTakes,
  onLauncherState,
  onLauncherVisibility,
  postToLauncher,
  tellLauncher,
  type LauncherPlayerState,
} from "./bridge";

export const LAUNCHER_VIDEO_CLASS = "tv-launcher-video";

/** Whether the launcher plays the video (a launcher whose hello lists play). */
export function launcherPlays(): boolean {
  return inLauncher() && launcherTakes("play");
}

/** What a stream session from /tv/ adds in a launcher: the TV's profile and its caps. */
export function launcherSessionFields(): {
  client_profile?: "tizen" | "webos";
  device_caps?: unknown;
} {
  const h = launcherPlays() ? launcherHello() : null;
  if (!h || (h.platform !== "tizen" && h.platform !== "webos")) return {};
  return { client_profile: h.platform, device_caps: h.device_caps };
}

export interface LauncherSource {
  /** The signed stream URL (the manifest, or the file itself). */
  url: string;
  startAt: number;
  subtitles: { lang: string; label?: string; url: string }[];
  mode?: "vod" | "live";
}

export interface LauncherHandle {
  detach(): void;
}

/** A URL the launcher can open: absolute, at the home's origin (its page is file://). */
function absolute(url: string): string {
  try {
    return new URL(url, window.location.href).href;
  } catch {
    return url;
  }
}

/** The subtitle size Watch set on an ancestor (--mkt-caption-scale), 1 by default. */
function captionScale(el: HTMLElement): number {
  const v = Number(getComputedStyle(el).getPropertyValue("--mkt-caption-scale").trim());
  return Number.isFinite(v) && v > 0 ? v : 1;
}

const OVERRIDDEN = [
  "currentTime",
  "duration",
  "paused",
  "ended",
  "readyState",
  "playbackRate",
  "audioTracks",
  "play",
  "pause",
] as const;

/**
 * Plays src through the launcher, with el as its stand-in. onError hears the player's
 * error, once per attachment.
 */
export function attachLauncher(
  el: HTMLVideoElement,
  src: LauncherSource,
  onError: (error: string) => void
): LauncherHandle {
  let position = src.startAt > 0 ? src.startAt : 0;
  let duration = 0;
  let paused = true;
  let ended = false;
  let buffering = true;
  let started = false;
  let seeking = false;
  let errored = false;
  let audio: NonNullable<LauncherPlayerState["audio_tracks"]> = [];
  let subtitleId: string | null = null;
  let scale = captionScale(el);
  const subs = src.subtitles.map((s, i) => ({
    id: `sub-${i}`,
    url: absolute(s.url),
    lang: s.lang,
    ...(s.label ? { label: s.label } : {}),
  }));
  const fire = (type: string) => el.dispatchEvent(new Event(type));

  const start = () => {
    onTrackChange(); // the track chosen before playback (Settings: subtitles on)
    started = true;
    postToLauncher({
      type: "maktaba:play",
      url: absolute(src.url),
      start_sec: position,
      subtitles: subs,
      audio: null,
      mode: src.mode ?? "vod",
      subtitle_scale: scale,
    });
    if (subtitleId) sendSubtitle();
  };

  const audioTracks = () =>
    audio.map((a, i) => ({
      id: String(i),
      label: a.label ?? "",
      language: a.lang ?? "",
      get enabled() {
        return a.enabled;
      },
      set enabled(on: boolean) {
        if (!on || a.enabled) return;
        audio = audio.map((x, j) => ({ ...x, enabled: j === i }));
        postToLauncher({ type: "maktaba:audioTrack", i });
      },
    }));

  const define = (name: (typeof OVERRIDDEN)[number], d: PropertyDescriptor) =>
    Object.defineProperty(el, name, { configurable: true, ...d });
  define("currentTime", {
    get: () => position,
    set: (t: number) => {
      if (!Number.isFinite(t)) return;
      position = Math.max(0, duration > 0 ? Math.min(t, duration) : t);
      ended = false;
      seeking = true;
      fire("seeking");
      if (started) postToLauncher({ type: "maktaba:seek", sec: position });
      fire("timeupdate");
    },
  });
  define("duration", { get: () => (duration > 0 ? duration : NaN) });
  define("paused", { get: () => paused });
  define("ended", { get: () => ended });
  define("readyState", { get: () => (duration > 0 ? 4 : 0) });
  define("playbackRate", { get: () => 1, set: () => undefined });
  define("audioTracks", { get: audioTracks });
  define("play", {
    value: () => {
      if (paused) {
        paused = false;
        ended = false;
        if (!started) start();
        else postToLauncher({ type: "maktaba:resume" });
        fire("play");
      }
      return Promise.resolve();
    },
  });
  define("pause", {
    value: () => {
      if (paused) return;
      paused = true;
      tellLauncher("maktaba:pause");
      fire("pause");
    },
  });

  // a text track shown on /tv/ is the launcher's to draw: the same track, by its index
  const tracks = el.textTracks;
  const sendSubtitle = () => {
    postToLauncher({ type: "maktaba:subtitle", id: subtitleId, subtitle_scale: scale });
  };
  const onTrackChange = () => {
    // the stand-in itself is invisible (tv.css), so its own rendering of the track shows nothing
    let id: string | null = null;
    for (let i = 0; i < (tracks?.length ?? 0); i++) {
      if (tracks[i]!.mode === "showing" && subs[i]) {
        id = subs[i]!.id;
        break;
      }
    }
    if (id === subtitleId) return;
    subtitleId = id;
    if (started) sendSubtitle();
  };
  tracks?.addEventListener?.("change", onTrackChange);

  const stopState = onLauncherState((s) => {
    if (s.error && !errored) {
      errored = true;
      onError(s.error);
      return;
    }
    if (Array.isArray(s.audio_tracks)) audio = s.audio_tracks;
    const hadDuration = duration > 0;
    if (s.duration > 0 && s.duration !== duration) {
      duration = s.duration;
      if (!hadDuration) fire("loadedmetadata");
      fire("durationchange");
    }
    if (s.buffering !== buffering) {
      buffering = s.buffering;
      if (!paused) fire(buffering ? "waiting" : "playing");
    }
    if (Number.isFinite(s.position)) position = s.position;
    if (seeking) {
      seeking = false;
      fire("seeked");
    }
    fire("timeupdate");
    if (s.ended && !ended) {
      ended = true;
      paused = true;
      fire("pause");
      fire("ended");
    }
    // the person's subtitle size, as Watch changes it
    const now = captionScale(el);
    if (now !== scale) {
      scale = now;
      if (started && subtitleId) sendSubtitle();
    }
  });
  const stopVisibility = onLauncherVisibility((hidden) => {
    if (hidden && !paused) {
      // the launcher paused its player when the TV hid the app
      paused = true;
      fire("pause");
    }
  });

  document.documentElement.classList.add(LAUNCHER_VIDEO_CLASS);
  if (el.autoplay) void el.play();

  return {
    detach() {
      stopState();
      stopVisibility();
      tracks?.removeEventListener?.("change", onTrackChange);
      if (started) tellLauncher("maktaba:stop");
      for (const name of OVERRIDDEN) delete (el as unknown as Record<string, unknown>)[name];
      document.documentElement.classList.remove(LAUNCHER_VIDEO_CLASS);
    },
  };
}
