// Epic 58 story 58.10 — the Samsung launcher in a real browser, opened from file:// as the
// packaged app is, with a `tizen` and `webapis` shim that includes a fake webapis.avplay:
//
//   - the hello answer carries the caps read from webapis (HDR10 and HLG, EAC3 passthrough
//     behind a Dolby Digital Plus output, the decoder's codecs), never Dolby Vision;
//   - a play message drives AVPlay: open, the whole screen letterboxed, the 4K mode for a
//     2160p source, prepare, play; the page behind the iframe turns transparent;
//   - Arabic subtitles (a sidecar WebVTT) are drawn by the launcher itself, right to left, at
//     the person's size, timed by AVPlay's play time;
//   - AVPlay's onerror reaches the UI as state.error, and the launcher says a converted
//     version follows, in English and Arabic;
//   - the red key and Play/Pause reach the UI as actions; Back (10009) too; Leave Maktaba
//     exits through tizen.application.
//
// The home's /tv/ is a small stand-in page here that speaks the bridge (the full /tv/ flow
// is launcher.ui.spec.ts). Screenshots are dark, in English and Arabic
// (test-results/launcher/shots); the launcher's log, the AVPlay calls and the bridge
// traffic are attached to the report.
import { expect, test, type Page, type Route, type TestInfo } from "@playwright/test";
import { mkdirSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const SHOTS = path.resolve(process.cwd(), "test-results/launcher/shots");
const LAUNCHER = pathToFileURL(path.resolve(process.cwd(), "dist-launcher/index.html")).href;
const HOME = "http://10.0.0.7:8088";
const UA =
  "Mozilla/5.0 (SMART-TV; LINUX; Tizen 6.5) AppleWebKit/537.36 (KHTML, like Gecko) 85.0.4183.93/6.5 TV Safari/537.36";

const VTT = `WEBVTT

00:00:01.000 --> 00:00:04.000
مرحبا بكم في مكتبة

00:00:05.000 --> 00:00:08.000
Second line
`;

/** The stand-in /tv/: says ready, keeps what the launcher sends, and is transparent. */
const TV_PAGE = `<!doctype html><html><head><meta name="color-scheme" content="dark"><style>html,body{background:transparent;margin:0}</style></head>
<body><script>
  window.__got = [];
  window.addEventListener("message", function (e) { window.__got.push(e.data); });
  parent.postMessage({ type: "maktaba:ready" }, "*");
</script></body></html>`;

/** A Samsung TV: tizen, webapis with a fake AVPlay that records its calls. */
function samsung() {
  if (location.protocol !== "file:") return;
  const w = window as unknown as Record<string, unknown>;
  const calls: string[] = [];
  let state = "NONE";
  w.__av = calls;
  w.tizen = {
    tvinputdevice: { registerKeyBatch: (k: string[]) => (w.__registered = k) },
    application: { getCurrentApplication: () => ({ exit: () => (w.__exited = "tizen") }) },
    tvaudiocontrol: { getOutputMode: () => "DOLBY_DIGITAL_PLUS" },
  };
  w.webapis = {
    network: { getIp: () => "10.0.0.5" },
    productinfo: {
      getRealModel: () => "QN90B",
      isUdPanelSupported: () => true,
      is8KPanelSupported: () => false,
    },
    avinfo: { isHdrTvSupport: () => true },
    avplay: {
      open: (url: string) => {
        calls.push(`open ${url}`);
        state = "IDLE";
      },
      close: () => {
        calls.push("close");
        state = "NONE";
      },
      setListener: (l: unknown) => {
        calls.push("setListener");
        w.__avListener = l;
      },
      setDisplayRect: (x: number, y: number, ww: number, h: number) =>
        calls.push(`setDisplayRect ${x},${y},${ww},${h}`),
      setDisplayMethod: (m: string) => calls.push(`setDisplayMethod ${m}`),
      setStreamingProperty: (n: string, v: string) => calls.push(`setStreamingProperty ${n} ${v}`),
      prepareAsync: (ok: () => void) => {
        calls.push("prepareAsync");
        state = "READY";
        setTimeout(ok, 10);
      },
      play: () => {
        calls.push("play");
        state = "PLAYING";
      },
      pause: () => calls.push("pause"),
      stop: () => {
        calls.push("stop");
        state = "IDLE";
      },
      seekTo: (ms: number, ok?: () => void) => {
        calls.push(`seekTo ${ms}`);
        if (ok) setTimeout(ok, 0);
      },
      getState: () => state,
      getDuration: () => 600_000,
      getCurrentTime: () => 0,
      getTotalTrackInfo: () => [
        { index: 0, type: "VIDEO" },
        { index: 1, type: "AUDIO" },
        { index: 2, type: "AUDIO" },
      ],
      setSelectTrack: (t: string, i: number) => calls.push(`setSelectTrack ${t} ${i}`),
      setSilentSubtitle: (s: boolean) => calls.push(`setSilentSubtitle ${s}`),
      suspend: () => calls.push("suspend"),
      restore: () => calls.push("restore"),
    },
  };
  // the TV's decoder: H.264, HEVC and AV1; AAC, AC3 and EAC3 (a QN90B's answers)
  const takes = ["avc1", "hvc1", "av01", "mp4a", "ac-3", "ec-3"];
  const ms = (window as unknown as { MediaSource?: { isTypeSupported: (t: string) => boolean } })
    .MediaSource;
  if (ms) ms.isTypeSupported = (t: string) => takes.some((c) => t.indexOf(c) >= 0);
}

async function lanHome(page: Page, log: string[]) {
  page.on("console", (m) => log.push(`[${m.type()}] ${m.text()}`));
  page.on("pageerror", (e) => log.push(`[pageerror] ${e.message}`));
  await page.route(/^http:\/\/10\.0\.0\.(?!7:)\d+:8088\//, (route) =>
    route.abort("connectionrefused")
  );
  const cors = { "Access-Control-Allow-Origin": "*" };
  await page.route(`${HOME}/api/discovery/home`, (route: Route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      headers: cors,
      body: JSON.stringify({
        id: "h-test",
        name: "Test home",
        version: "1.0.0",
        urls: [HOME],
        sign_in: { password: true, code: true },
        api_level: 1,
        api_level_oldest: 1,
        client_floor: null,
      }),
    })
  );
  await page.route(`${HOME}/tv/**`, (route) =>
    route.fulfill({ status: 200, contentType: "text/html", body: TV_PAGE })
  );
  await page.route(`${HOME}/subs/ar.vtt*`, (route) =>
    route.fulfill({ status: 200, contentType: "text/vtt", headers: cors, body: VTT })
  );
}

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  // the page is transparent while AVPlay plays (its video plane lies beneath it on a TV):
  // a dark frame stands in for the video here
  await page.evaluate(() => {
    document.documentElement.style.background = "linear-gradient(135deg, #1b2a3a, #0b111c)";
    // desktop Chromium has no AVPlay plugin and draws a placeholder where the TV draws video
    const plane = document.querySelector<HTMLElement>('object[type="application/avplayer"]');
    if (plane) plane.style.visibility = "hidden";
  });
  await page.evaluate(() => document.fonts?.ready);
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
}

async function keep(page: Page, info: TestInfo, console: string[]) {
  const read = (expr: string) =>
    page
      .evaluate((e) => {
        const w = window as unknown as Record<string, unknown>;
        if (e === "log") return (w.__maktabaLauncherLog as (() => string) | undefined)?.() ?? "";
        return JSON.stringify(w.__av ?? [], null, 1);
      }, expr)
      .catch(() => "(page closed)");
  await info.attach("launcher.log", { body: await read("log"), contentType: "text/plain" });
  await info.attach("avplay-calls.json", {
    body: await read("av"),
    contentType: "application/json",
  });
  await info.attach("console.log", { body: console.join("\n"), contentType: "text/plain" });
  const tv = page.frames().find((f) => f.url().startsWith(`${HOME}/tv/`));
  const got = tv
    ? await tv.evaluate(() =>
        JSON.stringify((window as unknown as { __got: unknown[] }).__got, null, 1)
      )
    : "[]";
  await info.attach("bridge-to-ui.json", { body: got, contentType: "application/json" });
}

for (const lang of ["en", "ar"] as const) {
  test(`samsung ${lang}: AVPlay, caps, subtitles drawn by Maktaba, errors and keys`, async ({
    browser,
  }, info) => {
    const context = await browser.newContext({
      userAgent: UA,
      colorScheme: "dark",
      viewport: { width: 1920, height: 1080 },
    });
    const page = await context.newPage();
    const console: string[] = [];
    await lanHome(page, console);
    try {
      await context.addInitScript(samsung);
      await context.addInitScript((l) => {
        try {
          if (location.protocol === "file:") localStorage.setItem("maktaba.launcher.lang", l);
        } catch {
          /* no storage: English */
        }
      }, lang);
      await page.goto(LAUNCHER);
      await expect(page.locator('[data-focus="find"]')).toBeFocused();
      await page.keyboard.press("Enter");
      const found = page.getByTestId("found-home");
      await expect(found).toContainText("Test home");
      await found.focus();
      await page.keyboard.press("Enter");
      await expect(page.getByTestId("tv-frame")).toBeVisible();
      const tv = page.frames().find((f) => f.url().startsWith(`${HOME}/tv/`))!;
      const got = () =>
        tv.evaluate(() => (window as unknown as { __got: Record<string, unknown>[] }).__got);
      const post = (m: Record<string, unknown>) =>
        tv.evaluate((msg) => window.parent.postMessage(msg, "*"), m);

      // hello: the caps webapis gave, in the API's shape; no Dolby Vision
      await post({ type: "maktaba:hello", id: 1, bridge_level: 1 });
      await expect
        .poll(async () => (await got()).some((m) => m.type === "maktaba:hello"))
        .toBe(true);
      const hello = (await got()).find((m) => m.type === "maktaba:hello") as {
        platform: string;
        device_caps: {
          video: { codec: string; max_height: number }[];
          hdr: string[];
          passthrough: string[];
          hls_segments: string[];
        };
      };
      expect(hello.platform).toBe("tizen");
      expect(hello.device_caps.video.map((v) => v.codec)).toEqual(["h264", "hevc", "av1"]);
      expect(hello.device_caps.video[0]!.max_height).toBe(2160);
      expect(hello.device_caps.hdr).toEqual(["hdr10", "hlg"]);
      expect(hello.device_caps.passthrough).toEqual(["ac3", "eac3"]);
      expect(hello.device_caps.hls_segments).toEqual(["ts", "fmp4"]);

      // play: AVPlay's sequence, the 4K mode for 2160p, the second audio track
      await post({
        type: "maktaba:play",
        url: `${HOME}/stream/v1/master.m3u8?sig=s1`,
        start_sec: 0,
        height: 2160,
        audio: 1,
        subtitles: [{ id: "ar", url: `${HOME}/subs/ar.vtt?sig=s2`, lang: "ar" }],
        subtitle_scale: 1.5,
      });
      await expect
        .poll(() => page.evaluate(() => (window as unknown as { __av: string[] }).__av))
        .toContain("play");
      const calls = await page.evaluate(() => (window as unknown as { __av: string[] }).__av);
      expect(calls.slice(0, 5)).toEqual([
        `open ${HOME}/stream/v1/master.m3u8?sig=s1`,
        "setListener",
        "setDisplayRect 0,0,1920,1080",
        "setDisplayMethod PLAYER_DISPLAY_MODE_LETTER_BOX",
        "setStreamingProperty ADAPTIVE_INFO FIXED_MAX_RESOLUTION=3840X2160",
      ]);
      expect(calls).toContain("setSelectTrack AUDIO 2");
      await expect(page.locator("body")).toHaveClass(/ml-avplay/);
      await expect(page.locator('object[type="application/avplayer"]')).toBeVisible();

      // Arabic subtitles, drawn by the launcher: right to left, at 1.5 x
      await post({ type: "maktaba:subtitle", id: "ar" });
      await expect
        .poll(() => page.evaluate(() => (window as unknown as { __av: string[] }).__av))
        .toContain("setSilentSubtitle true");
      await page.waitForTimeout(100);
      await page.evaluate(() =>
        (
          window as unknown as { __avListener: { oncurrentplaytime: (ms: number) => void } }
        ).__avListener.oncurrentplaytime(2000)
      );
      const subs = page.getByTestId("subtitles");
      await expect(subs).toBeVisible();
      await expect(subs).toContainText("مرحبا بكم في مكتبة");
      await expect(subs.locator("[dir=rtl]").first()).toBeVisible();
      await shot(page, `samsung-${lang}-subtitles`);

      // state reaches the UI; AVPlay's error becomes state.error and the notice
      await page.evaluate(() =>
        (
          window as unknown as { __avListener: { onerror: (t: string) => void } }
        ).__avListener.onerror("PLAYER_ERROR_NOT_SUPPORTED_FILE")
      );
      const notice = page.getByTestId("player-notice");
      await expect(notice).toBeVisible();
      await expect(notice).toHaveText(
        lang === "ar"
          ? "تعذّر على التلفاز تشغيل هذا. جارٍ تجربة نسخة محوّلة…"
          : "The TV couldn't play this. Trying a converted version…"
      );
      await expect
        .poll(async () =>
          (await got()).some(
            (m) =>
              m.type === "maktaba:state" && m.error === "avplay PLAYER_ERROR_NOT_SUPPORTED_FILE"
          )
        )
        .toBe(true);
      await shot(page, `samsung-${lang}-error`);

      // the remote: red, Play/Pause and Back reach the UI as actions
      for (const [keyCode, key] of [
        [403, "ColorF0Red"],
        [10252, "MediaPlayPause"],
        [10009, "XF86Back"],
      ] as const) {
        await page.evaluate(
          ([c, k]) =>
            window.dispatchEvent(
              new KeyboardEvent("keydown", {
                keyCode: c,
                key: k,
                bubbles: true,
              } as KeyboardEventInit)
            ),
          [keyCode, key] as const
        );
      }
      await expect
        .poll(async () =>
          (await got()).filter((m) => m.type === "maktaba:action").map((m) => m.action)
        )
        .toEqual(["menu", "playPause", "back"]);
      expect(
        await page.evaluate(() => (window as unknown as { __registered: string[] }).__registered)
      ).toEqual(expect.arrayContaining(["ColorF0Red", "MediaPlayPause", "MediaFastForward"]));

      // stop: AVPlay closed, the page opaque again; Leave Maktaba exits through tizen
      await post({ type: "maktaba:stop" });
      await expect(page.locator("body")).not.toHaveClass(/ml-avplay/);
      await post({ type: "maktaba:exit" });
      await expect
        .poll(() => page.evaluate(() => (window as unknown as { __exited?: string }).__exited))
        .toBe("tizen");
      // the signed URLs' sig never reaches the launcher's log
      const log = await page.evaluate(() =>
        (window as unknown as { __maktabaLauncherLog: () => string }).__maktabaLauncherLog()
      );
      expect(log).not.toContain("sig=s1");
      expect(log).not.toContain("sig=s2");
    } finally {
      await keep(page, info, console);
      await context.close();
    }
  });
}
