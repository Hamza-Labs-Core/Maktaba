// A fake of Google's Cast Web Sender SDK for the tests (story 58.06): there is no Cast
// emulator (research.md §5.1), so the sender is tested against this, and the pairing with a
// real receiver only on devices (docs/stores/cast.md).
import type { CastApi, CastLoadRequest, CastMediaInfo, CastSession } from "../lib/cast/sdk";

export class FakeSession implements CastSession {
  loads: CastLoadRequest[] = [];
  sent: { namespace: string; data: unknown }[] = [];
  listeners = new Map<string, (ns: string, msg: string) => void>();
  ended = false;
  constructor(public name = "Living room TV") {}
  getCastDevice() {
    return { friendlyName: this.name };
  }
  loadMedia(req: CastLoadRequest) {
    this.loads.push(req);
    return Promise.resolve();
  }
  sendMessage(namespace: string, data: unknown) {
    this.sent.push({ namespace, data });
    return Promise.resolve();
  }
  addMessageListener(ns: string, fn: (ns: string, msg: string) => void) {
    this.listeners.set(ns, fn);
  }
  removeMessageListener(ns: string) {
    this.listeners.delete(ns);
  }
  endSession() {
    this.ended = true;
  }
  /** The receiver speaks on a namespace. */
  receive(ns: string, data: unknown) {
    this.listeners.get(ns)?.(ns, JSON.stringify(data));
  }
}

export interface FakeCast {
  api: CastApi;
  options: { receiverApplicationId: string; autoJoinPolicy: string; language?: string }[];
  /** Chrome's picker: what requestSession does (connect a TV, or reject). */
  picker: "connect" | "cancel";
  session: FakeSession | null;
  castState: string;
  player: {
    isConnected: boolean;
    isMediaLoaded: boolean;
    isPaused: boolean;
    currentTime: number;
    duration: number;
    playerState: string | null;
    mediaInfo: CastMediaInfo | null;
  };
  controls: string[];
  /** Fires the context's events, as the SDK would. */
  setCastState(s: string): void;
  connect(session?: FakeSession): void;
  end(): void;
  /** The receiver's media status changed. */
  status(patch: Partial<FakeCast["player"]>): void;
}

export function fakeCast(): FakeCast {
  const ctxListeners = new Map<
    string,
    ((e: { sessionState?: string; castState?: string }) => void)[]
  >();
  const playerListeners: (() => void)[] = [];
  const fire = (type: string, e: { sessionState?: string; castState?: string }) =>
    (ctxListeners.get(type) ?? []).forEach((fn) => fn(e));
  const fc: FakeCast = {
    options: [],
    picker: "connect",
    session: null,
    castState: "NOT_CONNECTED",
    controls: [],
    player: {
      isConnected: false,
      isMediaLoaded: false,
      isPaused: true,
      currentTime: 0,
      duration: 0,
      playerState: null,
      mediaInfo: null,
    },
    setCastState(s) {
      fc.castState = s;
      fire("caststatechanged", { castState: s });
    },
    connect(session = new FakeSession()) {
      fc.session = session;
      fc.castState = "CONNECTED";
      fire("caststatechanged", { castState: "CONNECTED" });
      fire("sessionstatechanged", { sessionState: "SESSION_STARTED" });
    },
    end() {
      fc.session = null;
      fc.castState = "NOT_CONNECTED";
      fire("caststatechanged", { castState: "NOT_CONNECTED" });
      fire("sessionstatechanged", { sessionState: "SESSION_ENDED" });
    },
    status(patch) {
      Object.assign(fc.player, patch);
      playerListeners.forEach((fn) => fn());
    },
    api: undefined as unknown as CastApi,
  };
  const ctx = {
    setOptions: (o: FakeCast["options"][number]) => fc.options.push(o),
    requestSession: () => {
      if (fc.picker === "cancel") return Promise.reject(new Error("cancel"));
      fc.connect();
      return Promise.resolve(null);
    },
    getCurrentSession: () => fc.session,
    getCastState: () => fc.castState,
    addEventListener: (
      type: string,
      fn: (e: { sessionState?: string; castState?: string }) => void
    ) => ctxListeners.set(type, [...(ctxListeners.get(type) ?? []), fn]),
  };
  class MediaInfo implements CastMediaInfo {
    customData?: unknown;
    metadata?: CastMediaInfo["metadata"];
    constructor(
      public contentId: string,
      public contentType: string
    ) {}
  }
  class LoadRequest implements CastLoadRequest {
    autoplay?: boolean;
    currentTime?: number;
    constructor(public media: CastMediaInfo) {}
  }
  class GenericMediaMetadata {
    title?: string;
  }
  fc.api = {
    framework: {
      CastContext: { getInstance: () => ctx },
      RemotePlayer: function RemotePlayer() {
        return fc.player;
      } as unknown as CastApi["framework"]["RemotePlayer"],
      RemotePlayerController: function RemotePlayerController() {
        return {
          addEventListener: (_t: string, fn: () => void) => playerListeners.push(fn),
          playOrPause: () => fc.controls.push("playOrPause"),
          seek: () => fc.controls.push(`seek:${fc.player.currentTime}`),
          stop: () => fc.controls.push("stop"),
        };
      } as unknown as CastApi["framework"]["RemotePlayerController"],
      CastContextEventType: {
        SESSION_STATE_CHANGED: "sessionstatechanged",
        CAST_STATE_CHANGED: "caststatechanged",
      },
      SessionState: {
        SESSION_STARTED: "SESSION_STARTED",
        SESSION_RESUMED: "SESSION_RESUMED",
        SESSION_ENDED: "SESSION_ENDED",
      },
      CastState: {
        NO_DEVICES_AVAILABLE: "NO_DEVICES_AVAILABLE",
        NOT_CONNECTED: "NOT_CONNECTED",
        CONNECTING: "CONNECTING",
        CONNECTED: "CONNECTED",
      },
      RemotePlayerEventType: { ANY_CHANGE: "anyChanged" },
    },
    chromeCast: {
      AutoJoinPolicy: { ORIGIN_SCOPED: "origin_scoped" },
      media: { MediaInfo, LoadRequest, GenericMediaMetadata },
    },
  };
  return fc;
}
