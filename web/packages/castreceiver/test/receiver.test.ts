// T01 (story 58.07): the receiver's logic with a fake cast.framework and a fake home. A load
// opens a session with the grant and the device's report, picks TS or fMP4, reports progress
// every 10 s, renews before the URLs end, walks the queue, and shows the grant's end only at
// the end of an item.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { castData, NAMESPACE, PROGRESS_MS, publicData, Receiver } from "../src/receiver/receiver";
import { Screens } from "../src/receiver/ui";
import { CHROMECAST_3RD_GEN, FakeContext, fakeFramework, fakeHome, type Call } from "./fakeCaf";
import * as fx from "./fixtures";

function setup(
  opts: { routes?: Record<string, unknown>; device?: typeof CHROMECAST_3RD_GEN } = {}
) {
  document.body.innerHTML = '<div id="maktaba"></div>';
  const ctx = new FakeContext(opts.device);
  const fw = fakeFramework(ctx);
  const home = fakeHome({
    [`GET ${fx.HOME}/api/discovery/home`]: { body: fx.discovery },
    [`GET ${fx.HOME}/api/videos/${fx.VIDEO}`]: { body: fx.video },
    [`GET ${fx.HOME}/api/videos/${fx.VIDEO2}`]: { body: fx.video2 },
    [`GET ${fx.HOME}/api/videos/${fx.VIDEO}/chapters`]: { body: fx.chapters },
    [`GET ${fx.HOME}/api/videos/${fx.VIDEO2}/chapters`]: { body: [] },
    [`POST ${fx.HOME}/api/stream/sessions`]: { status: 201, body: fx.session() },
    [`POST ${fx.HOME}/api/stream/sessions/${fx.SESSION}/progress`]: { body: { accepted: true } },
    [`GET ${fx.HOME}/api/me/play-queues/${fx.QUEUE}`]: { body: fx.queue },
    [`PUT ${fx.HOME}/api/me/play-queues/${fx.QUEUE}/current`]: {
      body: { ...fx.queue, current_pos: 1, version: 4 },
    },
    ...(opts.routes as Record<string, never>),
  });
  const screens = new Screens(document.getElementById("maktaba") as HTMLElement, "/r/mark.svg");
  let handler: ((d: unknown, s: string) => void) | undefined;
  const receiver = new Receiver({
    framework: fw,
    context: ctx,
    screens,
    fetch: home.fetch,
    onMessage: (fn) => (handler = fn),
  });
  ctx.player.setMessageInterceptor("LOAD", (req) => receiver.onLoad(req));
  return { ctx, fw, home, screens, receiver, send: (d: unknown) => handler?.(d, "sender-1") };
}

function load(data: Record<string, unknown>) {
  return {
    media: {
      contentId: "x",
      customData: {
        home: fx.HOME,
        urls: [fx.HOME, fx.RELAY],
        grant: fx.grant(),
        lang: "en",
        api_level: 1,
        ...data,
      },
    },
  };
}

const sessions = (calls: Call[]) =>
  calls.filter((c) => c.method === "POST" && c.url.endsWith("/api/stream/sessions"));
const screen = () => (document.querySelector(".mk") as HTMLElement).dataset.screen;

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  globalThis.URL.createObjectURL = vi.fn(() => "blob:poster");
});
afterEach(() => {
  vi.useRealTimers();
});

describe("a load from a Maktaba sender (T01)", () => {
  it("opens a session with the grant, profile cast and the device's report", async () => {
    const { home, receiver } = setup();
    const g = fx.grant();
    const req = load({ video_id: fx.VIDEO, start_sec: 754, grant: g });
    const out = (await receiver.onLoad(req)) as typeof req & {
      media: Record<string, unknown>;
      currentTime: number;
    };
    const [open] = sessions(home.calls);
    expect(open.headers.Authorization).toBe(`Bearer ${g}`);
    expect(open.body).toMatchObject({ video_id: fx.VIDEO, client_profile: "cast", start_sec: 754 });
    const caps = (
      open.body as {
        device_caps: {
          video: { codec: string; max_height: number }[];
          hdr: string[];
          passthrough: string[];
        };
      }
    ).device_caps;
    expect(caps.video.map((v) => v.codec)).toEqual(["h264", "hevc"]);
    expect(caps.video.find((v) => v.codec === "hevc")?.max_height).toBe(2160);
    expect(caps.hdr).toEqual(["hdr10", "hlg", "dv-p5", "dv-p8"]);
    expect(caps.passthrough).toEqual(["eac3"]);
    // HEVC goes in fMP4, never TS
    expect(out.media.contentUrl).toBe(fx.session().manifest_url);
    expect(out.media.contentType).toBe("application/x-mpegurl");
    expect(out.media.hlsSegmentFormat).toBe("fmp4");
    expect(out.media.hlsVideoSegmentFormat).toBe("fmp4");
    expect(out.currentTime).toBe(754);
    // the media status every sender sees carries no grant and no address
    expect(JSON.stringify(out.media.customData)).not.toContain("cast_");
    expect(out.media.customData).toEqual({ video_id: fx.VIDEO, lang: "en" });
    expect(JSON.stringify(out)).not.toContain(g);
  });

  it("picks TS for H.264, and direct play is MP4", async () => {
    const { receiver } = setup({
      routes: {
        [`POST ${fx.HOME}/api/stream/sessions`]: {
          status: 201,
          body: fx.session({ current_rendition: { codec: "h264" } }),
        },
      },
    });
    const out = (await receiver.onLoad(load({ video_id: fx.VIDEO }))) as {
      media: Record<string, unknown>;
    };
    expect(out.media.hlsSegmentFormat).toBe("ts");
    expect(out.media.hlsVideoSegmentFormat).toBe("mpeg2_ts");

    const direct = setup({
      routes: {
        [`POST ${fx.HOME}/api/stream/sessions`]: {
          status: 201,
          body: fx.session({
            mode: "direct",
            manifest_url: undefined,
            direct_url: `${fx.HOME}/stream/x/file?sig=1`,
          }),
        },
      },
    });
    const d = (await direct.receiver.onLoad(load({ video_id: fx.VIDEO }))) as {
      media: Record<string, unknown>;
    };
    expect(d.media.contentType).toBe("video/mp4");
    expect(d.media.hlsSegmentFormat).toBeUndefined();
  });

  it("gives direct play its sidecar subtitles, signed with the session's stream_sig", async () => {
    const direct = fx.session({
      mode: "direct",
      manifest_url: undefined,
      direct_url: `${fx.HOME}/stream/${fx.SESSION}/file?sig=1`,
      stream_sig: "tok/en",
    });
    const { receiver } = setup({
      routes: {
        [`POST ${fx.HOME}/api/stream/sessions`]: { status: 201, body: direct },
        [`GET ${fx.HOME}/api/videos/${fx.VIDEO}/subtitles`]: {
          body: {
            items: [
              { language: "ar", is_default: true },
              { language: "en" },
              { language: "ar" },
              { language: "../x" },
            ],
          },
        },
      },
    });
    const out = (await receiver.onLoad(load({ video_id: fx.VIDEO }))) as {
      media: { tracks?: { trackContentId: string; language: string; type: string }[] };
    };
    expect(out.media.tracks?.map((t) => [t.language, t.trackContentId, t.type])).toEqual([
      ["ar", `${fx.HOME}/stream/${fx.SESSION}/subs/sidecar/ar.vtt?sig=tok%2Fen`, "TEXT"],
      ["en", `${fx.HOME}/stream/${fx.SESSION}/subs/sidecar/en.vtt?sig=tok%2Fen`, "TEXT"],
    ]);
    // a home older than 58.02 hands no stream_sig: no sidecar tracks
    const old = setup({
      routes: {
        [`POST ${fx.HOME}/api/stream/sessions`]: {
          status: 201,
          body: { ...direct, stream_sig: undefined },
        },
      },
    });
    const o = (await old.receiver.onLoad(load({ video_id: fx.VIDEO }))) as {
      media: { tracks?: unknown[] };
    };
    expect(o.media.tracks).toBeUndefined();
  });

  it("reports a Chromecast 3rd gen as H.264 at 1080p only", async () => {
    const { home, receiver } = setup({ device: CHROMECAST_3RD_GEN });
    await receiver.onLoad(load({ video_id: fx.VIDEO }));
    const caps = (sessions(home.calls)[0].body as { device_caps: Record<string, unknown> })
      .device_caps;
    expect(caps.video).toEqual([{ codec: "h264", max_height: 1080 }]);
    expect(caps.hdr).toEqual([]);
    expect(caps.max_audio_channels).toBe(2);
  });

  it("asks a home older than device_caps again without it", async () => {
    const { home, receiver } = setup({
      routes: {
        [`POST ${fx.HOME}/api/stream/sessions`]: (c: Call) =>
          (c.body as Record<string, unknown>).device_caps
            ? { status: 400, body: { detail: "unknown field" } }
            : { status: 201, body: fx.session() },
      },
    });
    const out = (await receiver.onLoad(load({ video_id: fx.VIDEO }))) as {
      media: Record<string, unknown>;
    };
    const opens = sessions(home.calls);
    expect(opens).toHaveLength(2);
    expect((opens[1].body as Record<string, unknown>).device_caps).toBeUndefined();
    expect((opens[1].body as Record<string, unknown>).client_profile).toBe("cast");
    expect(out.media.contentUrl).toBe(fx.session().manifest_url);
  });

  it("follows the sender's language: Arabic mirrors the page", async () => {
    const { receiver } = setup();
    await receiver.onLoad(load({ video_id: fx.VIDEO, lang: "ar" }));
    expect(document.documentElement.dir).toBe("rtl");
    expect(document.documentElement.lang).toBe("ar");
    await receiver.onLoad(load({ video_id: fx.VIDEO, lang: "en" }));
    expect(document.documentElement.dir).toBe("ltr");
  });

  it("lets a load that is not a Maktaba sender's play as it came", async () => {
    const { home, receiver } = setup();
    const req = { media: { contentId: "https://example.test/a.mp4", customData: { foo: 1 } } };
    expect(await receiver.onLoad(req)).toBe(req);
    expect(home.calls).toHaveLength(0);
  });
});

describe("while it plays", () => {
  it("reports progress every 10 seconds, and on pause", async () => {
    const { ctx, home, receiver } = setup();
    await ctx.player.load(load({ video_id: fx.VIDEO }));
    ctx.player.emit("PLAYING");
    expect(screen()).toBe("playing");
    ctx.player.time = 30;
    await vi.advanceTimersByTimeAsync(PROGRESS_MS);
    ctx.player.time = 40;
    await vi.advanceTimersByTimeAsync(PROGRESS_MS);
    const progress = () => home.calls.filter((c) => c.url.endsWith("/progress"));
    expect(progress().map((c) => (c.body as { position_sec: number }).position_sec)).toEqual([
      30, 40,
    ]);
    ctx.player.time = 45;
    ctx.player.emit("PAUSE");
    await vi.advanceTimersByTimeAsync(0);
    expect(progress().at(-1)?.body).toEqual({ position_sec: 45 });
    expect(screen()).toBe("paused");
    expect(document.querySelector(".mk-paused-title")?.textContent).toBe(fx.video.title);
    expect(document.querySelector(".mk-paused-text")?.textContent).toBe(fx.video.description);
    void receiver;
  });

  it("shows the title, the position with left-to-right times, and the chapter", async () => {
    const { ctx } = setup();
    await ctx.player.load(load({ video_id: fx.VIDEO, lang: "ar" }));
    ctx.player.emit("PLAYING");
    ctx.player.time = 754;
    ctx.player.duration = 5400;
    ctx.player.emit("TIME_UPDATE");
    expect(document.querySelector(".mk-title")?.textContent).toBe(fx.video.title);
    expect(document.querySelector(".mk-pos")?.textContent).toBe("‎12:34‎ من ‎1:30:00‎");
    expect(document.querySelector(".mk-chapter")?.textContent).toBe("الفصل: The storm");
  });

  it("renews the signed URLs before they end, at the position", async () => {
    const { ctx, home } = setup({
      routes: {
        [`POST ${fx.HOME}/api/stream/sessions`]: {
          status: 201,
          body: fx.session({ expires_at: new Date(Date.now() + 5 * 60_000).toISOString() }),
        },
      },
    });
    await ctx.player.load(load({ video_id: fx.VIDEO, start_sec: 10 }));
    ctx.player.emit("PLAYING");
    ctx.player.time = 200;
    await vi.advanceTimersByTimeAsync(3 * 60_000 + PROGRESS_MS);
    const opens = sessions(home.calls);
    expect(opens.length).toBeGreaterThanOrEqual(2);
    expect((opens[1].body as { start_sec: number }).start_sec).toBe(200);
    // the picture stays: no loading screen for a renewal
    expect(screen()).toBe("playing");
    expect(ctx.player.loads.at(-1)?.currentTime).toBe(200);
  });

  it("walks the queue: Up next near the end, then the next item", async () => {
    const { ctx, home } = setup();
    await ctx.player.load(load({ queue_id: fx.QUEUE }));
    expect(sessions(home.calls)[0].body).toMatchObject({ video_id: fx.VIDEO });
    ctx.player.emit("PLAYING");
    expect(document.querySelector(".mk-subtitle")?.textContent).toBe("Harbour Tales");
    ctx.player.duration = 5400;
    ctx.player.time = 5390;
    ctx.player.emit("TIME_UPDATE");
    expect((document.querySelector(".mk-upnext") as HTMLElement).hidden).toBe(false);
    expect(document.querySelector(".mk-upnext-title")?.textContent).toBe("Episode Two");
    ctx.player.emit("MEDIA_FINISHED");
    await vi.advanceTimersByTimeAsync(0);
    await vi.waitFor(() => expect(sessions(home.calls)).toHaveLength(2));
    const put = home.calls.find((c) => c.method === "PUT");
    expect(put?.body).toEqual({ pos: 1, version: 3 });
    expect(sessions(home.calls)[1].body).toMatchObject({ video_id: fx.VIDEO2 });
    expect(
      home.calls.some(
        (c) => c.url.endsWith("/progress") && (c.body as { completed?: boolean }).completed
      )
    ).toBe(true);
  });

  it("lets the item play out when the grant ends, then shows the grant's end", async () => {
    const { ctx, home } = setup({
      routes: {
        [`POST ${fx.HOME}/api/stream/sessions/${fx.SESSION}/progress`]: { status: 401, body: {} },
      },
    });
    await ctx.player.load(load({ queue_id: fx.QUEUE }));
    ctx.player.emit("PLAYING");
    await vi.advanceTimersByTimeAsync(PROGRESS_MS);
    // still playing: never stopped mid-film for the grant's sake
    expect(screen()).toBe("playing");
    ctx.player.emit("MEDIA_FINISHED");
    await vi.waitFor(() => expect(screen()).toBe("error"));
    expect(document.querySelector(".mk-error-text")?.textContent).toBe(
      "This cast has ended. Cast again from your phone."
    );
    expect(sessions(home.calls)).toHaveLength(1);
  });

  it("asks once for a transcode when the TV cannot play the stream, then shows the format error", async () => {
    const { ctx, home } = setup();
    await ctx.player.load(load({ video_id: fx.VIDEO }));
    ctx.player.emit("PLAYING");
    ctx.player.emit("ERROR");
    await vi.waitFor(() => expect(sessions(home.calls)).toHaveLength(2));
    expect(sessions(home.calls)[1].body).toMatchObject({ force_transcode: true });
    ctx.player.emit("ERROR");
    await vi.waitFor(() => expect(screen()).toBe("error"));
    expect(document.querySelector(".mk-error-text")?.textContent).toContain(
      "can't play this video"
    );
  });

  it("takes a fresh grant from the sender on the namespace", async () => {
    const { ctx, home, send } = setup();
    await ctx.player.load(load({ video_id: fx.VIDEO }));
    ctx.player.emit("PLAYING");
    const fresh = fx.grant(Math.floor(Date.now() / 1000) + 7200);
    send({ type: "grant", grant: fresh });
    await vi.advanceTimersByTimeAsync(PROGRESS_MS);
    expect(
      home.calls.filter((c) => c.url.endsWith("/progress")).at(-1)?.headers.Authorization
    ).toBe(`Bearer ${fresh}`);
    expect(ctx.sent.every((m) => m.namespace === NAMESPACE)).toBe(true);
    expect(ctx.sent.some((m) => (m.data as { state?: string }).state === "playing")).toBe(true);
  });
});

describe("a load that fails", () => {
  it("shows that the TV cannot reach the home, and tells the sender", async () => {
    const { ctx, receiver } = setup({
      routes: {
        [`GET ${fx.HOME}/api/discovery/home`]: { hang: true },
        [`GET ${fx.RELAY}/api/discovery/home`]: { status: 502 },
      },
    });
    const out = (await receiver.onLoad(load({ video_id: fx.VIDEO }))) as {
      type: string;
      customData: unknown;
    };
    expect(out.type).toBe("LOAD_FAILED");
    expect(out.customData).toEqual({ error: "home" });
    expect(screen()).toBe("error");
    expect(document.querySelector(".mk-error-text")?.textContent).toContain(
      "can't reach your home"
    );
    expect(ctx.sent.some((m) => (m.data as { error?: string }).error === "home")).toBe(true);
  });

  it("shows the grant's end for a grant the home refuses", async () => {
    const { receiver } = setup({
      routes: {
        [`GET ${fx.HOME}/api/videos/${fx.VIDEO}`]: { status: 403, body: { code: "cast_scope" } },
      },
    });
    const out = (await receiver.onLoad(load({ video_id: fx.VIDEO }))) as { customData: unknown };
    expect(out.customData).toEqual({ error: "grant" });
  });
});

describe("the custom data", () => {
  it("needs a home, a cast grant and an item", () => {
    expect(castData({ home: fx.HOME, grant: "cast_x", video_id: fx.VIDEO })).toMatchObject({
      lang: "en",
    });
    expect(castData({ home: fx.HOME, grant: "eyJ", video_id: fx.VIDEO })).toBeNull();
    expect(castData({ home: fx.HOME, grant: "cast_x" })).toBeNull();
    expect(castData(null)).toBeNull();
    expect(
      castData({ home: fx.HOME, grant: "cast_x", queue_id: fx.QUEUE, lang: "ar-EG", start_sec: -4 })
    ).toMatchObject({
      lang: "ar",
      start_sec: undefined,
    });
  });

  it("is passed on to the senders without its grant or address", () => {
    const d = castData({ home: fx.HOME, grant: "cast_x", queue_id: fx.QUEUE, lang: "ar" });
    expect(publicData(d!, fx.VIDEO, 1)).toEqual({
      video_id: fx.VIDEO,
      lang: "ar",
      queue_id: fx.QUEUE,
      queue_pos: 1,
    });
  });
});
