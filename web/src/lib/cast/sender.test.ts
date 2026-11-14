// Story 58.06 T04 and the web sender's flow, against a fake of Google's SDK (src/test/fakeCast).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api";
import { fakeCast, type FakeCast } from "../../test/fakeCast";
import { CAST_NAMESPACE, CAST_SDK_URL, castBlocked, loadCastSdk, resetCastSdk } from "./sdk";
import { CastSender, GRANT_REFRESH_BEFORE_MS, type SenderDeps } from "./sender";

const CHROME =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36";
const FIREFOX =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:132.0) Gecko/20100101 Firefox/132.0";
const SAFARI =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15";

function win(origin: string, ua = CHROME) {
  const u = new URL(origin);
  return {
    isSecureContext: u.protocol === "https:",
    location: { origin: u.origin, protocol: u.protocol } as Location,
    navigator: { userAgent: ua } as Navigator,
  };
}

const LAN = "https://192-168-1-20.h1.direct.maktaba.tv:8444";
const RELAY = "https://h1.relay.maktaba.tv";
const VIDEO = "8f0c2b1e-0000-4000-8000-000000000001";
const QUEUE = "5a1d0c3e-0000-4000-8000-000000000002";

let fc: FakeCast;
let posts: { path: string; body: unknown }[];
let timers: { fn: () => void; ms: number }[];
let mintAnswer: () => Promise<unknown>;

function deps(over: Partial<SenderDeps> = {}): SenderDeps {
  return {
    appId: "ABCD1234",
    loadSdk: () => Promise.resolve(fc.api),
    post: <T>(path: string, body: unknown) => {
      posts.push({ path, body });
      return mintAnswer() as Promise<T>;
    },
    get: <T>() =>
      Promise.resolve({ urls: ["http://192.168.1.20:8088", LAN, RELAY], api_level: 1 } as T),
    win: win(LAN),
    inCloud: false,
    lang: () => "ar",
    now: () => Date.parse("2026-11-14T10:00:00Z"),
    setTimer: (fn, ms) => timers.push({ fn, ms }),
    clearTimer: () => {},
    ...over,
  };
}

beforeEach(() => {
  fc = fakeCast();
  posts = [];
  timers = [];
  mintAnswer = () =>
    Promise.resolve({ grant: "cast_G1", expires_at: "2026-11-14T16:00:00Z", scope: "cast" });
});

describe("castBlocked: where the button may show (T04)", () => {
  it("shows on Chrome over https", () => expect(castBlocked("ABCD1234", win(RELAY))).toBeNull());
  it("hides on a home reached at http://", () =>
    expect(castBlocked("ABCD1234", win("http://192.168.1.20:8088"))).toBe("insecure"));
  it("hides without a registered receiver", () =>
    expect(castBlocked("", win(RELAY))).toBe("no-app"));
  it("hides on Firefox, Safari and Chrome on iOS", () => {
    expect(castBlocked("ABCD1234", win(RELAY, FIREFOX))).toBe("browser");
    expect(castBlocked("ABCD1234", win(RELAY, SAFARI))).toBe("browser");
    const chromeIOS =
      "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0.0.0 Mobile/15E148 Safari/604.1";
    expect(castBlocked("ABCD1234", win(RELAY, chromeIOS))).toBe("browser");
  });
});

describe("loadCastSdk", () => {
  afterEach(() => resetCastSdk());
  it("adds Google's script once and answers the SDK when it is available", async () => {
    const w: Record<string, unknown> = {};
    const p = loadCastSdk(document, w as never);
    expect(loadCastSdk(document, w as never)).toBe(p);
    const scripts = [...document.head.querySelectorAll("script")].filter(
      (s) => s.src === CAST_SDK_URL
    );
    expect(scripts).toHaveLength(1);
    w.cast = { framework: fc.api.framework };
    w.chrome = { cast: fc.api.chromeCast };
    (w.__onGCastApiAvailable as (a: boolean) => void)(true);
    await expect(p).resolves.toEqual(fc.api);
    scripts[0].remove();
  });
  it("answers null when the script says Cast is not available", async () => {
    const w: Record<string, unknown> = {};
    const p = loadCastSdk(document, w as never);
    (w.__onGCastApiAvailable as (a: boolean) => void)(false);
    await expect(p).resolves.toBeNull();
    document.head.querySelectorAll("script").forEach((s) => s.src === CAST_SDK_URL && s.remove());
  });
});

describe("CastSender", () => {
  it("never loads the SDK on a plain-HTTP home, through Maktaba Cloud, or without an app id", async () => {
    const loadSdk = vi.fn(() => Promise.resolve(fc.api));
    for (const d of [
      deps({ loadSdk, win: win("http://192.168.1.20:8088") }),
      deps({ loadSdk, inCloud: true }),
      deps({ loadSdk, appId: "" }),
    ]) {
      const s = new CastSender(d);
      await s.start();
      expect(s.current.available).toBe(false);
    }
    expect(loadSdk).not.toHaveBeenCalled();
  });

  it("hides the button when the home has no address a TV can reach", async () => {
    const s = new CastSender(
      deps({
        win: win("https://192.168.1.20:8444"),
        get: <T>() => Promise.resolve({ urls: ["http://192.168.1.20:8088"], api_level: 1 } as T),
      })
    );
    await s.start();
    expect(s.current).toMatchObject({ available: false, blocked: "unreachable" });
  });

  it("starts the context with Maktaba's receiver and the page's language", async () => {
    const s = new CastSender(deps());
    await s.start();
    expect(s.current).toMatchObject({ available: true, blocked: null, state: "idle" });
    expect(fc.options).toEqual([
      { receiverApplicationId: "ABCD1234", autoJoinPolicy: "origin_scoped", language: "ar" },
    ]);
  });

  it("casts a video: picker, grant, the load's custom data (no token, no header)", async () => {
    const s = new CastSender(deps());
    expect(await s.cast({ videoId: VIDEO, startSec: 754.4, title: "Lesson 1" })).toBe(true);
    expect(posts).toEqual([{ path: "/api/cast/grants", body: { video_id: VIDEO } }]);
    const load = fc.session!.loads[0];
    expect(load.media.contentId).toBe(VIDEO);
    expect(load.media.customData).toEqual({
      home: LAN,
      urls: [LAN, RELAY],
      grant: "cast_G1",
      video_id: VIDEO,
      start_sec: 754,
      lang: "ar",
      api_level: 1,
    });
    expect(load.currentTime).toBe(754);
    expect(load.autoplay).toBe(true);
    expect(load.media.metadata?.title).toBe("Lesson 1");
    expect(JSON.stringify(load)).not.toMatch(/Bearer|Authorization|headers/i);
    expect(s.current).toMatchObject({
      device: "Living room TV",
      state: "connected",
      videoId: VIDEO,
    });
  });

  it("casts the server queue it plays (Play all, Shuffle)", async () => {
    const s = new CastSender(deps({ lang: () => "en" }));
    await s.cast({ videoId: VIDEO, queueId: QUEUE, queuePos: 4, startSec: 0 });
    expect(posts[0].body).toEqual({ queue_id: QUEUE });
    expect(fc.session!.loads[0].media.customData).toMatchObject({
      queue_id: QUEUE,
      queue_pos: 4,
      lang: "en",
    });
    expect(fc.session!.loads[0].media.customData).not.toHaveProperty("video_id");
  });

  it("casts nothing when the picker is closed", async () => {
    fc.picker = "cancel";
    const s = new CastSender(deps());
    expect(await s.cast({ videoId: VIDEO, startSec: 0 })).toBe(false);
    expect(posts).toEqual([]);
  });

  it.each([
    [
      new ApiError({ type: "x", title: "forbidden", status: 403, code: "cast_via_cloud" }),
      "cast.error.cloud",
    ],
    [
      new ApiError({ type: "access-denied", title: "access denied", status: 403 }),
      "cast.error.refused",
    ],
    [new ApiError({ type: "not-found", title: "not found", status: 404 }), "cast.error.refused"],
    [new ApiError({ type: "x", title: "boom", status: 500 }), "cast.error.failed"],
  ])("a refused grant loads nothing and says why (%s)", async (err, key) => {
    mintAnswer = () => Promise.reject(err);
    const s = new CastSender(deps());
    expect(await s.cast({ videoId: VIDEO, startSec: 0 })).toBe(false);
    expect(fc.session!.loads).toEqual([]);
    expect(s.current.error).toBe(key);
  });

  it("follows the TV: the queue's next video, position, pause; then the end of the cast", async () => {
    const s = new CastSender(deps());
    await s.cast({ videoId: VIDEO, queueId: QUEUE, queuePos: 0, startSec: 0 });
    fc.status({
      isPaused: false,
      currentTime: 61,
      duration: 1200,
      mediaInfo: {
        contentId: "next",
        contentType: "video/mp4",
        customData: { video_id: "next", queue_id: QUEUE, queue_pos: 1, lang: "ar" },
      },
    });
    expect(s.current).toMatchObject({
      videoId: "next",
      queuePos: 1,
      paused: false,
      currentTime: 61,
      duration: 1200,
    });
    s.playOrPause();
    s.seekTo(90);
    expect(fc.controls).toEqual(["playOrPause", "seek:90"]);
    fc.end();
    expect(s.current).toMatchObject({ device: null, state: "idle" });
  });

  it("shows the receiver's errors", async () => {
    const s = new CastSender(deps());
    await s.cast({ videoId: VIDEO, startSec: 0 });
    fc.session!.receive(CAST_NAMESPACE, { type: "error", error: "grant" });
    expect(s.current.error).toBe("cast.error.grantEnded");
    fc.session!.receive(CAST_NAMESPACE, { type: "state", state: "playing" });
    expect(s.current.error).toBe("cast.error.grantEnded");
  });

  it("hands the receiver a fresh grant 30 minutes before the first ends", async () => {
    const s = new CastSender(deps());
    await s.cast({ videoId: VIDEO, startSec: 0 });
    expect(timers).toHaveLength(1);
    // 06:00 left on a grant minted at 10:00 → renew at 15:30
    expect(timers[0].ms).toBe(6 * 3600_000 - GRANT_REFRESH_BEFORE_MS);
    mintAnswer = () => Promise.resolve({ grant: "cast_G2", expires_at: "2026-11-14T22:00:00Z" });
    timers[0].fn();
    await vi.waitFor(() => expect(fc.session!.sent).toHaveLength(1));
    expect(fc.session!.sent[0]).toEqual({
      namespace: CAST_NAMESPACE,
      data: { type: "grant", grant: "cast_G2" },
    });
    expect(posts.map((p) => p.body)).toEqual([{ video_id: VIDEO }, { video_id: VIDEO }]);
    expect(timers).toHaveLength(2);
  });

  it("joins a cast this origin left running (a reload)", async () => {
    fc.session = new (await import("../../test/fakeCast")).FakeSession("Bedroom");
    fc.castState = "CONNECTED";
    fc.player.mediaInfo = {
      contentId: VIDEO,
      contentType: "video/mp4",
      customData: { video_id: VIDEO, lang: "en" },
    };
    const s = new CastSender(deps());
    await s.start();
    expect(s.current).toMatchObject({ device: "Bedroom", state: "connected", videoId: VIDEO });
  });

  it("on no TV found, the state says none", async () => {
    const s = new CastSender(deps());
    await s.start();
    fc.setCastState("NO_DEVICES_AVAILABLE");
    expect(s.current.state).toBe("none");
  });
});
