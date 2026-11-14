// Epic 58 story 58.09 T04 — the Samsung and LG launcher, opened from file:// as a packaged
// app is, with a Tizen or webOS shim, against a mock home on the TV's /24:
//
//   - the subnet probe finds the home, which is opened in the iframe at /tv/?bridge=1;
//   - /tv/ asks the launcher's hello over the bridge, and, signed out, shows the home's own
//     code (plain HTTP on the LAN: the screen says so); approved, it signs in by bearer
//     (every API call carries the token: the frame never gets the session cookie);
//   - a play message runs the launcher's player; Leave Maktaba exits through the platform;
//   - the saved home opens at once the next time; Switch home brings the launcher back.
//
// Screenshots are dark, in English and Arabic (test-results/launcher/shots); the launcher's
// own log (window.__maktabaLauncherLog), the console, the network and the home's calls are
// attached to the report.
import { expect, test, type BrowserContext, type Page, type Route, type TestInfo } from "@playwright/test";
import { mkdirSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { mockHome, type MockHome } from "../tv/mockHome";

const SHOTS = path.resolve(process.cwd(), "test-results/launcher/shots");
const LAUNCHER = pathToFileURL(path.resolve(process.cwd(), "dist-launcher/index.html")).href;
const HOME = "http://10.0.0.7:8088";
const TV_UI = "http://localhost:5177";
const TOKEN = "tv-access-1";

const UA = {
  tizen:
    "Mozilla/5.0 (SMART-TV; LINUX; Tizen 5.5) AppleWebKit/537.36 (KHTML, like Gecko) 69.0.3497.106.1/5.5 TV Safari/537.36",
  webos:
    "Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.79 Safari/537.36 WebAppManager",
};

type Platform = keyof typeof UA;

/** The TV's own APIs, on the launcher's page only (the iframe is the home's). */
function shim(platform: Platform) {
  const w = window as unknown as Record<string, unknown>;
  if (location.protocol !== "file:") return;
  if (platform === "tizen") {
    w.tizen = {
      tvinputdevice: { registerKeyBatch: (k: string[]) => (w.__registered = k) },
      application: { getCurrentApplication: () => ({ exit: () => (w.__exited = "tizen") }) },
    };
    w.webapis = {
      network: { getIp: () => "10.0.0.5" },
      productinfo: { getRealModel: () => "QE55Q80T", isUdPanelSupported: () => true },
    };
  } else {
    w.webOS = {
      service: {
        request: (_uri: string, req: { onSuccess: (r: unknown) => void }) =>
          setTimeout(() => req.onSuccess({ wired: { state: "connected", ipAddress: "10.0.0.5" } })),
      },
      deviceInfo: (cb: (d: unknown) => void) => cb({ modelName: "OLED55CX", uhd: true }),
      platformBack: () => (w.__exited = "webos"),
    };
  }
}

interface Trail {
  console: string[];
  network: string[];
  bearer: string[];
  approved: boolean;
}

/** The home on the LAN: discovery (open to any origin), /tv/, pairing and the bearer gate. */
async function lanHome(page: Page): Promise<{ home: MockHome; trail: Trail }> {
  const trail: Trail = { console: [], network: [], bearer: [], approved: false };
  page.on("console", (m) => trail.console.push(`[${m.type()}] ${m.text()}`));
  page.on("pageerror", (e) => trail.console.push(`[pageerror] ${e.message}`));
  page.on("requestfinished", async (r) => {
    const res = await r.response();
    trail.network.push(`${res?.status() ?? "-"} ${r.method()} ${r.url()}`);
  });
  const home = await mockHome(page);
  // the rest of the /24 (every address but the home's): nothing answers
  await page.route(/^http:\/\/10\.0\.0\.(?!7:)\d+:8088\//, (route) =>
    route.abort("connectionrefused")
  );
  const json = (route: Route, status: number, body: unknown, cors = false) =>
    route.fulfill({
      status,
      contentType: "application/json",
      headers: cors ? { "Access-Control-Allow-Origin": "*" } : {},
      body: JSON.stringify(body),
    });
  // the home's discovery answer, as GET /api/discovery/home gives it
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
  // /tv/ is the home's (dist/tv from vite preview)
  await page.route(`${HOME}/tv/**`, async (route) => {
    const res = await route.fetch({ url: route.request().url().replace(HOME, TV_UI) });
    await route.fulfill({ response: res });
  });
  // the API: the bearer gate, then the mock home's tables
  await page.route(`${HOME}/api/**`, async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const auth = (await req.headerValue("authorization")) ?? "";
    if (auth) trail.bearer.push(`${req.method()} ${url.pathname} ${auth === `Bearer ${TOKEN}` ? "ok" : auth}`);
    if (url.pathname === "/api/pairing/device" && req.method() === "POST")
      return json(route, 200, {
        code: "KQ7M-4R2P",
        device_code: "dc-test",
        expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
        verify_path: "/pair",
        verify_url: `${HOME}/pair?code=KQ7M-4R2P`,
        interval: 1,
      });
    if (url.pathname === "/api/pairing/device/token")
      return trail.approved
        ? json(route, 200, {
            access_token: TOKEN,
            access_expires_in: 900,
            refresh_token: "tv-refresh-1",
            refresh_expires_in: 2592000,
          })
        : json(route, 202, { status: "pending", looked_up: true });
    if (url.pathname === "/api/auth/me" && auth !== `Bearer ${TOKEN}`)
      return json(route, 401, { title: "Unauthorized", status: 401 });
    return route.fallback();
  });
  return { home, trail };
}

async function openLauncher(context: BrowserContext, page: Page, platform: Platform, lang: "en" | "ar") {
  await context.addInitScript(shim, platform);
  await context.addInitScript((l) => {
    try {
      if (location.protocol === "file:" && !localStorage.getItem("maktaba.launcher.lang"))
        localStorage.setItem("maktaba.launcher.lang", l);
    } catch {
      /* no storage: English */
    }
  }, lang);
  await page.goto(LAUNCHER);
}

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.evaluate(() => document.fonts?.ready);
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
}

async function keep(page: Page, info: TestInfo, home: MockHome, trail: Trail) {
  const log = await page
    .evaluate(() => {
      const l = (window as unknown as { __maktabaLauncherLog?: () => string }).__maktabaLauncherLog;
      return typeof l === "function" ? l() : String(l ?? "");
    })
    .catch(() => "(page closed)");
  await info.attach("launcher.log", { body: log, contentType: "text/plain" });
  await info.attach("console.log", { body: trail.console.join("\n"), contentType: "text/plain" });
  await info.attach("network.log", { body: trail.network.join("\n"), contentType: "text/plain" });
  await info.attach("home-calls.log", { body: home.calls.join("\n"), contentType: "text/plain" });
  await info.attach("bearer.log", { body: trail.bearer.join("\n"), contentType: "text/plain" });
}

const tvFrame = (page: Page) => page.frameLocator('[data-testid="tv-frame"]');

for (const platform of ["tizen", "webos"] as const) {
  for (const lang of ["en", "ar"] as const) {
    test(`${platform} ${lang}: probe, code sign-in by bearer, play, leave`, async ({ browser }, info) => {
      const context = await browser.newContext({
        userAgent: UA[platform],
        colorScheme: "dark",
        viewport: { width: 1920, height: 1080 },
      });
      const page = await context.newPage();
      const { home, trail } = await lanHome(page);
      try {
        await openLauncher(context, page, platform, lang);
        await expect(page.locator("html")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");

        // no saved home: the start screen; Find on this network (focused) probes the TV's /24
        await shot(page, `${platform}-${lang}-start`);
        await expect(page.locator('[data-focus="find"]')).toBeFocused();
        await page.keyboard.press("Enter");
        const found = page.getByTestId("found-home");
        await expect(found).toContainText("Test home");
        await shot(page, `${platform}-${lang}-found`);
        await found.focus();
        await page.keyboard.press("Enter");

        // /tv/ in the frame, signed out: the home's code, plain HTTP said
        const frame = tvFrame(page);
        await expect(frame.getByTestId("tv-code")).toHaveText("KQ7M-4R2P");
        await expect(frame.getByTestId("tv-plain")).toBeVisible();
        await expect(frame.locator("main")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");
        await expect(page.getByTestId("tv-frame")).toBeVisible();
        await shot(page, `${platform}-${lang}-code`);

        // approved on a phone: signed in by bearer, the big screen opens
        trail.approved = true;
        await expect(frame.locator('[data-testid="big-home"], [data-bs-focus]').first()).toBeVisible({
          timeout: 20_000,
        });
        expect(trail.bearer.some((l) => l.startsWith("GET /api/auth/me ok"))).toBe(true);
        expect(trail.bearer.every((l) => l.endsWith(" ok"))).toBe(true);
        await shot(page, `${platform}-${lang}-home`);

        // the hello answer named the TV: the client header is the TV app's
        const hello = await page.evaluate(() =>
          (window as unknown as { __maktabaLauncherLog?: () => string }).__maktabaLauncherLog?.()
        );
        expect(hello ?? "").toContain("hello");

        // a play message from /tv/ runs the launcher's player
        const tvWin = page.frames().find((f) => f.url().startsWith(`${HOME}/tv/`))!;
        await tvWin.evaluate((url) => {
          window.parent.postMessage({ type: "maktaba:play", url, start_sec: 0 }, "*");
        }, `${HOME}/stream/v1/master.m3u8`);
        await expect
          .poll(() => page.evaluate(() => document.querySelector("video")?.getAttribute("src") ?? ""))
          .toContain("/stream/v1/master.m3u8");

        // Leave Maktaba (the UI's confirm) exits through the TV's API
        await tvWin.evaluate(() => window.parent.postMessage({ type: "maktaba:exit" }, "*"));
        await expect
          .poll(() => page.evaluate(() => (window as unknown as { __exited?: string }).__exited))
          .toBe(platform);
        if (platform === "tizen")
          expect(
            await page.evaluate(() => (window as unknown as { __registered?: string[] }).__registered)
          ).toContain("MediaPlayPause");

        // the next start opens the saved home at once; Switch home is the launcher's picker
        await page.reload();
        await expect(page.getByTestId("tv-frame")).toBeVisible();
        await tvWin.page().waitForTimeout(0);
        const again = page.frames().find((f) => f.url().startsWith(`${HOME}/tv/`))!;
        await again.evaluate(() => window.parent.postMessage({ type: "maktaba:openHomePicker" }, "*"));
        await expect(page.getByTestId("saved-home")).toContainText("Test home");
        await expect(page.getByTestId("tv-frame")).toHaveCount(0);
        await shot(page, `${platform}-${lang}-saved`);
        expect(home.unknown).toEqual([]);
      } finally {
        await keep(page, info, home, trail);
        await context.close();
      }
    });
  }
}
