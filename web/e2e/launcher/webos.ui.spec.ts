// Story 58.11 T04 — LG webOS in a real browser: the launcher opened from file:// with a
// webOS shim (webOS.deviceInfo, luna, platformBack) and a saved home already signed in, then
// /tv/ playing a video through the launcher's native <video>:
//
//   - the stream session names the TV (client_profile webos) and its decoders (device_caps
//     from the shim's answers, story 58.02);
//   - the launcher's <video> (under the iframe) gets the signed URL, absolute; /tv/'s own
//     element is a stand-in and its page goes transparent;
//   - Arabic subtitles chosen in Watch are drawn by the launcher's layer, right to left;
//   - a player error reopens the stream once as a transcode (force_transcode);
//   - Back on Home, after the confirm, leaves through webOS.platformBack.
//
// The first stream is a file the browser cannot open, so its error runs the transcode retry;
// the second is a VP9 still that Playwright's Chromium plays. The TV's real media pipeline
// (HEVC, HDR, native HLS) is the device checklist's
// (docs/stores/lg.md, T05). Screenshots dark, English and Arabic; the launcher's log, the
// console, network and the home's calls are kept in the report.
import { expect, test, type Page, type Route, type TestInfo } from "@playwright/test";
import { mkdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { mockHome, type MockHome } from "../tv/mockHome";

const SHOTS = path.resolve(process.cwd(), "test-results/launcher/shots");
const LAUNCHER = pathToFileURL(path.resolve(process.cwd(), "dist-launcher/index.html")).href;
const HOME = "http://10.0.0.7:8088";
const TV_UI = "http://localhost:5177";
const TOKEN = "tv-access-webos";
const UA =
  "Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.270 Safari/537.36 WebAppManager";
// a 30-second still (VP9, which Playwright's Chromium decodes) for the stream after the retry
const LESSON = readFileSync(path.resolve(process.cwd(), "e2e/launcher/fixtures/lesson.webm"));
const VTT_AR = "WEBVTT\n\n00:00:00.000 --> 00:10:00.000\nبسم الله نبدأ الدرس\n";

/** webOS's APIs on the launcher's page, and a saved, signed-in home in each origin. */
function seed(args: { home: string; token: string }) {
  const w = window as unknown as Record<string, unknown>;
  if (location.protocol === "file:") {
    w.webOS = {
      service: {
        request: (_uri: string, req: { onSuccess: (r: unknown) => void }) =>
          setTimeout(() => req.onSuccess({ wired: { state: "connected", ipAddress: "10.0.0.5" } })),
      },
      deviceInfo: (cb: (d: unknown) => void) =>
        cb({
          modelName: "OLED55C3",
          sdkVersion: "8.0.0",
          uhd: true,
          hdr10: true,
          dolbyVision: true,
        }),
      platformBack: () => (w.__exited = "webos"),
    };
    localStorage.setItem(
      "maktaba.launcher.homes",
      JSON.stringify([
        { id: "h-test", name: "Test home", base: args.home, plain: true, lastUsed: Date.now() },
      ])
    );
  } else if (location.origin === args.home) {
    localStorage.setItem(
      "mkt:tv:session",
      JSON.stringify({ access: args.token, accessUntil: Date.now() + 3_600_000, refresh: "r" })
    );
  }
}

interface Trail {
  console: string[];
  network: string[];
  sessions: Record<string, unknown>[];
}

async function home(page: Page): Promise<{ home: MockHome; trail: Trail }> {
  const trail: Trail = { console: [], network: [], sessions: [] };
  page.on("console", (m) => trail.console.push(`[${m.type()}] ${m.text()}`));
  page.on("pageerror", (e) => trail.console.push(`[pageerror] ${e.message}`));
  page.on("requestfinished", async (r) => {
    const res = await r.response();
    trail.network.push(`${res?.status() ?? "-"} ${r.method()} ${r.url()}`);
  });
  const mock = await mockHome(page);
  const json = (route: Route, status: number, body: unknown, cors = false) =>
    route.fulfill({
      status,
      contentType: "application/json",
      headers: cors ? { "Access-Control-Allow-Origin": "*" } : {},
      body: JSON.stringify(body),
    });
  await page.route(`${HOME}/api/discovery/home`, (route) =>
    json(
      route,
      200,
      {
        id: "h-test",
        name: "Test home",
        version: "1.0.0",
        urls: [HOME],
        sign_in: { password: true, code: true },
        api_level: 1,
        api_level_oldest: 1,
        client_floor: null,
      },
      true
    )
  );
  await page.route(`${HOME}/tv/**`, async (route) => {
    const res = await route.fetch({ url: route.request().url().replace(HOME, TV_UI) });
    await route.fulfill({ response: res });
  });
  // the stream: a session per open, its file (never decodable here) and its Arabic subtitles
  await page.route(`${HOME}/api/stream/sessions`, async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    const body = route.request().postDataJSON() as Record<string, unknown>;
    trail.sessions.push(body);
    const n = trail.sessions.length;
    return json(route, 200, {
      session_id: `s${n}`,
      mode: body.force_transcode ? "transcode" : "direct",
      // the first: a file the TV cannot open; the transcode after it plays
      direct_url:
        n === 1 ? `/stream/s1/file.mp4?sig=sig1` : `/stream/s${n}/lesson.webm?sig=sig${n}`,
      manifest_url:
        n === 1 ? `/stream/s1/file.mp4?sig=sig1` : `/stream/s${n}/lesson.webm?sig=sig${n}`,
      expires_at: new Date(Date.now() + 3_600_000).toISOString(),
      duration_sec: 600,
      subtitles: [{ lang: "ar", label: "العربية", url: `/stream/s${n}/subs/ar.vtt` }],
    });
  });
  await page.route(`${HOME}/stream/**`, (route) => {
    const url = route.request().url();
    if (url.includes("/subs/ar.vtt"))
      return route.fulfill({
        status: 200,
        contentType: "text/vtt",
        headers: { "Access-Control-Allow-Origin": "*" },
        body: VTT_AR,
      });
    if (url.includes("/lesson.webm"))
      return route.fulfill({ status: 200, contentType: "video/webm", body: LESSON });
    return route.fulfill({ status: 404, body: "" });
  });
  return { home: mock, trail };
}

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.evaluate(() => document.fonts?.ready);
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
}

async function keep(page: Page, info: TestInfo, mock: MockHome, trail: Trail) {
  const log = await page
    .evaluate(
      () =>
        (window as unknown as { __maktabaLauncherLog?: () => string }).__maktabaLauncherLog?.() ??
        ""
    )
    .catch(() => "(page closed)");
  await info.attach("launcher.log", { body: log, contentType: "text/plain" });
  await info.attach("console.log", { body: trail.console.join("\n"), contentType: "text/plain" });
  await info.attach("network.log", { body: trail.network.join("\n"), contentType: "text/plain" });
  await info.attach("home-calls.log", { body: mock.calls.join("\n"), contentType: "text/plain" });
  await info.attach("sessions.json", {
    body: JSON.stringify(trail.sessions, null, 2),
    contentType: "application/json",
  });
}

const tvFrame = (page: Page) => page.frames().find((f) => f.url().startsWith(`${HOME}/tv/`));

for (const lang of ["en", "ar"] as const) {
  test(`webos ${lang}: plays through the TV's <video>, Arabic subtitles, transcode retry, Back`, async ({
    browser,
  }, info) => {
    const context = await browser.newContext({
      userAgent: UA,
      colorScheme: "dark",
      viewport: { width: 1920, height: 1080 },
    });
    const page = await context.newPage();
    const { home: mock, trail } = await home(page);
    try {
      await context.addInitScript(seed, { home: HOME, token: TOKEN });
      await context.addInitScript((l) => {
        if (location.protocol === "file:") localStorage.setItem("maktaba.launcher.lang", l);
      }, lang);
      await page.goto(LAUNCHER);

      // the saved home opens at once, signed in
      await expect(page.getByTestId("tv-frame")).toBeVisible();
      await expect
        .poll(
          async () =>
            (await tvFrame(page)?.locator('[data-testid="big-home"], [data-bs-focus]').count()) ??
            0,
          {
            timeout: 20_000,
          }
        )
        .toBeGreaterThan(0);

      // open a video's player in /tv/ (as a card's Play does)
      await tvFrame(page)!.evaluate(() => {
        history.pushState({ bs: 1 }, "", "/tv/big/watch/v1");
        window.dispatchEvent(new PopStateEvent("popstate", { state: { bs: 1 } }));
      });

      // the session names the TV and what it decodes
      await expect.poll(() => trail.sessions.length).toBeGreaterThan(0);
      expect(trail.sessions[0]).toMatchObject({
        video_id: "v1",
        client_profile: "webos",
        device_caps: {
          containers: ["mp4", "mkv", "ts"],
          hdr: ["hdr10", "hlg"],
          hls_segments: ["ts", "fmp4"],
          dash: false,
        },
      });
      // 2023 (webOS 23) is not a year the device checklist marked good for Dolby Vision
      expect((trail.sessions[0]!.device_caps as { hdr: string[] }).hdr).not.toContain("dv-p8");

      // the launcher's own <video> has the signed URL; /tv/ is transparent over it
      await expect
        .poll(() =>
          page.evaluate(() => document.querySelector("video.ml-plane")?.getAttribute("src") ?? "")
        )
        .toContain(`${HOME}/stream/s1/file.mp4?sig=sig1`);
      const tv = tvFrame(page)!;
      await expect(tv.locator("html.tv-launcher-video")).toHaveCount(1);

      // the person's error (the file cannot play here): the stream opens again as a transcode
      await expect.poll(() => trail.sessions.length, { timeout: 20_000 }).toBeGreaterThan(1);
      expect(trail.sessions[1]).toMatchObject({
        video_id: "v1",
        client_profile: "webos",
        force_transcode: true,
      });
      await expect
        .poll(() =>
          page.evaluate(() => document.querySelector("video.ml-plane")?.getAttribute("src") ?? "")
        )
        .toContain("/stream/s2/lesson.webm?sig=sig2");
      // the transcode plays: the launcher's state moves /tv/'s clock, and no error shows
      await expect
        .poll(
          () =>
            page.evaluate(
              () => document.querySelector<HTMLVideoElement>("video.ml-plane")?.currentTime ?? 0
            ),
          {
            timeout: 15_000,
          }
        )
        .toBeGreaterThan(0.5);
      await expect
        .poll(() => tv.evaluate(() => document.querySelector("video")?.currentTime ?? 0))
        .toBeGreaterThan(0.5);
      await expect(tv.locator(".mkt-player__error")).toHaveCount(0);

      // Subtitles → Arabic in Watch: the launcher draws the cue, right to left
      await tv.evaluate(() => {
        const v = document.querySelector("video") as HTMLVideoElement;
        const tracks = v.textTracks;
        for (let i = 0; i < tracks.length; i++) tracks[i]!.mode = i === 0 ? "showing" : "disabled";
      });
      const cue = page.locator(".ml-subs .ml-cue");
      await expect(cue).toHaveText("بسم الله نبدأ الدرس");
      await expect(cue).toHaveAttribute("dir", "rtl");
      await page.keyboard.press("ArrowDown"); // the controls show over the picture
      await shot(page, `webos-${lang}-watch`);

      // leaving the player stops the launcher's; Leave Maktaba exits by platformBack
      await tv.evaluate(() => history.back());
      await expect
        .poll(() =>
          page.evaluate(() => document.querySelector("video.ml-plane")?.hasAttribute("src"))
        )
        .toBe(false);
      await expect(tv.locator("html.tv-launcher-video")).toHaveCount(0);
      await tv.evaluate(() => window.parent.postMessage({ type: "maktaba:exit" }, "*"));
      await expect
        .poll(() => page.evaluate(() => (window as unknown as { __exited?: string }).__exited))
        .toBe("webos");
    } finally {
      await keep(page, info, mock, trail);
      await context.close();
    }
  });
}
