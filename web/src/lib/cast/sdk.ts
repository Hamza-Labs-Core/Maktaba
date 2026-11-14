// The slice of Google's Cast Web Sender SDK the web player uses (story 58.06), typed here so
// the tests can stand a fake in for it (there is no Cast emulator: research.md §5.1). The SDK
// is Google's script, loaded only where it can work: Chrome, on an HTTPS page, with Maktaba's
// receiver registered (shared/cast/app.json). Everywhere else the player shows no Cast button.

/** Google's sender script; it calls window.__onGCastApiAvailable once it knows. */
export const CAST_SDK_URL =
  "https://www.gstatic.com/cv/js/sender/v1/cast_sender.js?loadCastFramework=1";

/** The receiver's custom message namespace (shared/cast/app.json). */
export const CAST_NAMESPACE = "urn:x-cast:tv.maktaba.cast";

export interface CastDevice {
  friendlyName: string;
}

export interface CastMediaInfo {
  contentId: string;
  contentType: string;
  customData?: unknown;
  metadata?: { title?: string; images?: { url: string }[] } | null;
}

export interface CastLoadRequest {
  media: CastMediaInfo;
  autoplay?: boolean;
  currentTime?: number;
}

export interface CastSession {
  getCastDevice(): CastDevice;
  loadMedia(req: CastLoadRequest): Promise<unknown>;
  sendMessage(namespace: string, data: unknown): Promise<unknown>;
  addMessageListener(namespace: string, fn: (namespace: string, message: string) => void): void;
  removeMessageListener(namespace: string, fn: (namespace: string, message: string) => void): void;
  endSession(stopCasting: boolean): void;
}

export interface CastContext {
  setOptions(opts: {
    receiverApplicationId: string;
    autoJoinPolicy: string;
    language?: string;
  }): void;
  requestSession(): Promise<unknown>;
  getCurrentSession(): CastSession | null;
  getCastState(): string;
  addEventListener(
    type: string,
    fn: (e: { sessionState?: string; castState?: string }) => void
  ): void;
}

export interface RemotePlayer {
  isConnected: boolean;
  isMediaLoaded: boolean;
  isPaused: boolean;
  currentTime: number;
  duration: number;
  playerState: string | null;
  mediaInfo: CastMediaInfo | null;
}

export interface RemotePlayerController {
  addEventListener(type: string, fn: () => void): void;
  playOrPause(): void;
  seek(): void;
  stop(): void;
}

/** window.cast.framework, as far as Maktaba uses it. */
export interface CastFramework {
  CastContext: { getInstance(): CastContext };
  RemotePlayer: new () => RemotePlayer;
  RemotePlayerController: new (p: RemotePlayer) => RemotePlayerController;
  CastContextEventType: { SESSION_STATE_CHANGED: string; CAST_STATE_CHANGED: string };
  SessionState: { SESSION_STARTED: string; SESSION_RESUMED: string; SESSION_ENDED: string };
  CastState: {
    NO_DEVICES_AVAILABLE: string;
    NOT_CONNECTED: string;
    CONNECTING: string;
    CONNECTED: string;
  };
  RemotePlayerEventType: { ANY_CHANGE: string };
}

/** window.chrome.cast, as far as Maktaba uses it. */
export interface ChromeCast {
  AutoJoinPolicy: { ORIGIN_SCOPED: string };
  media: {
    MediaInfo: new (contentId: string, contentType: string) => CastMediaInfo;
    LoadRequest: new (media: CastMediaInfo) => CastLoadRequest;
    GenericMediaMetadata: new () => { title?: string; images?: { url: string }[] };
  };
}

export interface CastApi {
  framework: CastFramework;
  chromeCast: ChromeCast;
}

interface CastWindow {
  __onGCastApiAvailable?: (available: boolean) => void;
  cast?: { framework?: CastFramework };
  chrome?: { cast?: ChromeCast };
}

/**
 * Why this page cannot cast, or null when it may try to load the SDK: no receiver registered,
 * a page reached over plain HTTP (the SDK needs a secure page, and a receiver page could not
 * load the home's plain-HTTP media anyway), or a browser that is not Chrome.
 */
export function castBlocked(
  appId: string,
  win: Pick<Window, "isSecureContext" | "location" | "navigator">
): "no-app" | "insecure" | "browser" | null {
  if (!appId) return "no-app";
  if (!win.isSecureContext || win.location.protocol !== "https:") return "insecure";
  const ua = win.navigator.userAgent;
  // Chrome and the Chromium browsers that ship Chrome's Cast support; Firefox, Safari and
  // Chrome on iOS (CriOS, Safari's engine) never load the framework, and the script itself
  // says so for the rest
  if (!/\bChrome\//.test(ua) || /\b(Firefox|FxiOS)\//.test(ua)) return "browser";
  return null;
}

let loading: Promise<CastApi | null> | null = null;

/**
 * Loads Google's sender script once and answers the SDK, or null where it is not available
 * (the script said so, failed to load, or took more than timeoutMs).
 */
export function loadCastSdk(
  doc: Document = document,
  win: CastWindow = window as unknown as CastWindow,
  timeoutMs = 10_000
): Promise<CastApi | null> {
  if (loading) return loading;
  loading = new Promise<CastApi | null>((resolve) => {
    const done = (ok: boolean) => {
      clearTimeout(timer);
      const fw = win.cast?.framework;
      const cc = win.chrome?.cast;
      resolve(ok && fw && cc ? { framework: fw, chromeCast: cc } : null);
    };
    const timer = setTimeout(() => done(false), timeoutMs);
    win.__onGCastApiAvailable = (available) => done(available);
    const s = doc.createElement("script");
    s.src = CAST_SDK_URL;
    s.async = true;
    s.onerror = () => done(false);
    doc.head.appendChild(s);
  });
  return loading;
}

/** For the tests: forget the loaded SDK. */
export function resetCastSdk() {
  loading = null;
}
