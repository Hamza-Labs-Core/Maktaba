// Epic 58 story 58.09 T04 — the Samsung and LG launcher, opened from file:// as a packaged
// app is, with a Tizen or webOS shim, against a mock home on the TV's /24:
//
//   - the subnet probe finds the home, which is opened in the iframe at /tv/?bridge=1;
//   - /tv/ asks the launcher's hello over the bridge, and, signed out, shows the home's own
//     code (plain HTTP on the LAN: the screen says so); approved, it signs in by bearer
//     (every API call carries the token: the frame never gets the session cookie);
//   - a play message runs the launcher's player; Leave Maktaba exits through the platform;
//   - the saved home opens at once the next time; Switch home brings the launcher back;
//   - the cloud code (58.09's second part) is the first screen: approved on a phone, the
//     launcher reaches the home through the relay the cloud names and hands its /tv/ the
//     grant over the bridge, which /tv/ redeems in the home's origin (no home code asked).
//
// Screenshots are dark, in English and Arabic (test-results/launcher/shots); the launcher's
// own log (window.__maktabaLauncherLog), the console, the network and the home's calls are
// attached to the report.
import {
  expect,
  test,
  type BrowserContext,
  type Page,
  type Route,
  type TestInfo,
} from "@playwright/test";
import { mkdirSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { mockHome, type MockHome } from "../tv/mockHome";

const SHOTS = path.resolve(process.cwd(), "test-results/launcher/shots");
const LAUNCHER = pathToFileURL(path.resolve(process.cwd(), "dist-launcher/index.html")).href;
const HOME = "http://10.0.0.7:8088";
const TV_UI = path.resolve(process.cwd(), "dist/tv");
const TOKEN = "tv-access-1";
const RELAY = "https://h-test.relay.maktaba.tv";
// the relay's address as a pattern (every character a pattern gives meaning to escaped)
const RELAY_RE = RELAY.replace(/[\\^$.*+?()[\]{}|/]/g, "\\$&");
const CLOUD = "https://api.maktaba.tv";
const GRANT = "Gr4nt_e2e_" + "k".repeat(33);
const POLL_SECRET = "poll-secret-e2e-0123456789";

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
          void Promise.resolve().then(() =>
            req.onSuccess({ wired: { state: "connected", ipAddress: "10.0.0.5" } })
          ),
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
  /** The cloud code was approved on a phone: the next poll hands the TV its payload. */
  cloudApproved: boolean;
  /** The Origin each cloud call carried (what the TV's runtime sends from file://). */
  cloudOrigins: string[];
  /** The grants the home's token route was asked to redeem. */
  redeemed: string[];
}

/** The home on the LAN: discovery (open to any origin), /tv/, pairing and the bearer gate. */
async function lanHome(page: Page): Promise<{ home: MockHome; trail: Trail }> {
  const trail: Trail = {
    console: [],
    network: [],
    bearer: [],
    approved: false,
    cloudApproved: false,
    cloudOrigins: [],
    redeemed: [],
  };
  page.on("console", (m) => trail.console.push(`[${m.type()}] ${m.text()}`));
  page.on("pageerror", (e) => trail.console.push(`[pageerror] ${e.message}`));
  page.on("response", (r) =>
    trail.network.push(`${r.status()} ${r.request().method()} ${r.url()}`)
  );
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
  // Maktaba Cloud's TV code routes (story 58.03), answering the TV app's origin as
  // cloud/internal/middleware/tvcors.go does: echoed, never with credentials
  await page.route(`${CLOUD}/v1/tv/**`, async (route) => {
    const req = route.request();
    const origin = (await req.headerValue("origin")) ?? "";
    trail.cloudOrigins.push(`${req.method()} ${new URL(req.url()).pathname} ${origin}`);
    const cors = { "Access-Control-Allow-Origin": origin, Vary: "Origin" };
    if (req.method() === "OPTIONS")
      return route.fulfill({
        status: 204,
        headers: {
          ...cors,
          "Access-Control-Allow-Methods": "POST, OPTIONS",
          "Access-Control-Allow-Headers": "Content-Type, X-Maktaba-Client",
        },
      });
    const body = (status: number, b: unknown) =>
      route.fulfill({
        status,
        contentType: "application/json",
        headers: cors,
        body: JSON.stringify(b),
      });
    const path = new URL(req.url()).pathname;
    if (path === "/v1/tv/codes")
      return body(201, {
        code: "ABCD-2345",
        poll_secret: POLL_SECRET,
        expires_in: 600,
        interval: 1,
        verify_url: "https://maktaba.tv/tv?c=ABCD2345",
      });
    if (path === "/v1/tv/codes/poll")
      return trail.cloudApproved
        ? body(200, {
            status: "approved",
            // the cloud's server id: the home answers with its own instance id (h-test)
            home_id: "5e1d7c2a-9b3f-4e8d-a6c1-0f2b3d4e5a6b",
            home_name: "Test home",
            // the LAN name answers nothing here (a TV at a relative's house): the relay does
            connections: [
              { url: "https://10-0-0-7.h-test.direct.example:8443", kind: "local" },
              {
                url: "https://10.0.0.7:8443",
                kind: "local_ip",
                tls_name: "10-0-0-7.h-test.direct.example",
              },
              { url: RELAY, kind: "relay" },
            ],
            grant: GRANT,
          })
        : body(202, { status: "pending", interval: 1 });
    return body(404, { error: "not_found" });
  });
  await page.route(/^https:\/\/(10-0-0-7\.h-test\.direct\.example|10\.0\.0\.7):8443\//, (route) =>
    route.abort("connectionrefused")
  );
  for (const base of [HOME, RELAY]) await homeAt(page, base, trail, json);
  return { home, trail };
}

/** The home at base: discovery (open to any origin), /tv/, pairing and the bearer gate. */
async function homeAt(
  page: Page,
  base: string,
  trail: Trail,
  json: (route: Route, status: number, body: unknown, cors?: boolean) => Promise<void>
) {
  // the home's discovery answer, as GET /api/discovery/home gives it
  await page.route(`${base}/api/discovery/home`, (route) =>
    json(
      route,
      200,
      {
        id: "h-test",
        name: "Test home",
        version: "1.0.0",
        urls: [base],
        sign_in: { password: true, code: true },
        api_level: 1,
        api_level_oldest: 1,
        client_floor: null,
      },
      true
    )
  );
  // /tv/ is the home's (dist/tv, read from disk: a fetch from the preview server could still
  // be running when a test that is done closes its context, and fail it)
  await page.route(`${base}/tv/**`, (route) => {
    const file = new URL(route.request().url()).pathname.slice("/tv/".length) || "index.html";
    return route.fulfill({ path: path.join(TV_UI, file) });
  });
  // the API: the bearer gate, then the mock home's tables
  await page.route(`${base}/api/**`, async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const auth = (await req.headerValue("authorization")) ?? "";
    if (auth)
      trail.bearer.push(
        `${req.method()} ${url.pathname} ${auth === `Bearer ${TOKEN}` ? "ok" : auth}`
      );
    if (url.pathname === "/api/pairing/device" && req.method() === "POST")
      return json(route, 200, {
        code: "KQ7M-4R2P",
        device_code: "dc-test",
        expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
        verify_path: "/pair",
        verify_url: `${base}/pair?code=KQ7M-4R2P`,
        interval: 1,
      });
    if (url.pathname === "/api/pairing/device/token") {
      const code = String((req.postDataJSON() as { device_code?: unknown })?.device_code ?? "");
      if (code === GRANT) {
        // a cloud code's grant: single use, as the home's own pairing secret is
        trail.redeemed.push(code);
        return trail.redeemed.length === 1
          ? json(route, 200, {
              access_token: TOKEN,
              access_expires_in: 900,
              refresh_token: "tv-refresh-1",
              refresh_expires_in: 2592000,
            })
          : json(route, 410, { title: "Gone", status: 410 });
      }
      return trail.approved
        ? json(route, 200, {
            access_token: TOKEN,
            access_expires_in: 900,
            refresh_token: "tv-refresh-1",
            refresh_expires_in: 2592000,
          })
        : json(route, 202, { status: "pending", looked_up: true });
    }
    if (url.pathname === "/api/auth/me" && auth !== `Bearer ${TOKEN}`)
      return json(route, 401, { title: "Unauthorized", status: 401 });
    return route.fallback();
  });
}

/**
 * The language is the TV's own (the context's locale). No init script may touch the launcher's
 * localStorage: on a file:// page Chromium can answer so early a read with an empty store, not
 * the page's own, for as long as the document lives, and a home saved there (or one to be read
 * there) is gone. The launcher's deferred script reads it late enough.
 */
async function openLauncher(context: BrowserContext, page: Page, platform: Platform) {
  await context.addInitScript(shim, platform);
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
  await info.attach("cloud.log", {
    body: trail.cloudOrigins.join("\n"),
    contentType: "text/plain",
  });
}

const tvFrame = (page: Page) => page.frameLocator('[data-testid="tv-frame"]');

for (const platform of ["tizen", "webos"] as const) {
  for (const lang of ["en", "ar"] as const) {
    test(`${platform} ${lang}: probe, code sign-in by bearer, play, leave`, async ({
      browser,
    }, info) => {
      const context = await browser.newContext({
        userAgent: UA[platform],
        locale: lang,
        colorScheme: "dark",
        viewport: { width: 1920, height: 1080 },
      });
      const page = await context.newPage();
      const { home, trail } = await lanHome(page);
      try {
        await openLauncher(context, page, platform);
        await expect(page.locator("html")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");

        // no saved home: the start screen, the cloud code first; Find on this network
        // (focused) probes the TV's /24
        await expect(page.getByTestId("cloud-code")).toHaveText("ABCD-2345");
        await shot(page, `${platform}-${lang}-start`);
        await expect(page.locator('[data-focus="find"]')).toBeFocused();
        // an address gets 700 ms to answer (probe.test.ts times it): the page's clock waits here,
        // so a slow machine's mock home is not taken for one that is not there
        await page.clock.install();
        await page.clock.pauseAt(Date.now() + 1000);
        await page.keyboard.press("Enter");
        const found = page.getByTestId("found-home");
        await expect(found).toContainText("Test home");
        await page.clock.resume();
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
        await expect(
          frame.locator('[data-testid="big-home"], [data-bs-focus]').first()
        ).toBeVisible({
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
          .poll(() =>
            page.evaluate(() => document.querySelector("video")?.getAttribute("src") ?? "")
          )
          .toContain("/stream/v1/master.m3u8");

        // Leave Maktaba (the UI's confirm) exits through the TV's API
        await tvWin.evaluate(() => window.parent.postMessage({ type: "maktaba:exit" }, "*"));
        await expect
          .poll(() => page.evaluate(() => (window as unknown as { __exited?: string }).__exited))
          .toBe(platform);
        if (platform === "tizen")
          expect(
            await page.evaluate(
              () => (window as unknown as { __registered?: string[] }).__registered
            )
          ).toContain("MediaPlayPause");

        // the next start opens the saved home at once; Switch home is the launcher's picker
        await page.reload();
        await expect(page.getByTestId("tv-frame")).toBeVisible();
        await tvWin.page().waitForTimeout(0);
        const again = page.frames().find((f) => f.url().startsWith(`${HOME}/tv/`))!;
        await again.evaluate(() =>
          window.parent.postMessage({ type: "maktaba:openHomePicker" }, "*")
        );
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

for (const platform of ["tizen", "webos"] as const) {
  for (const lang of ["en", "ar"] as const) {
    test(`${platform} ${lang}: the cloud code, approved on a phone, signs in through the relay`, async ({
      browser,
    }, info) => {
      const context = await browser.newContext({
        userAgent: UA[platform],
        locale: lang,
        colorScheme: "dark",
        viewport: { width: 1920, height: 1080 },
      });
      const page = await context.newPage();
      const { home, trail } = await lanHome(page);
      try {
        await openLauncher(context, page, platform);
        await expect(page.locator("html")).toHaveAttribute("dir", lang === "ar" ? "rtl" : "ltr");

        // the first screen: the cloud code, its QR, and the other ways under it
        await expect(page.getByTestId("cloud-code")).toHaveText("ABCD-2345");
        await expect(page.getByTestId("cloud-qr")).toBeVisible();
        await expect(page.getByTestId("cloud-code")).toHaveAttribute("dir", "ltr");
        await expect(page.locator('[data-focus="find"]')).toBeVisible();
        await expect(page.getByTestId("more-ways")).toBeVisible();
        await shot(page, `${platform}-${lang}-cloud-code`);
        // the TV's packaged page calls the cloud from its own origin (Chromium's file://: null)
        expect(trail.cloudOrigins.some((l) => l.startsWith("POST /v1/tv/codes "))).toBe(true);

        // approved on a phone (Profile → Approve a TV): the relay answers as the home, its
        // /tv/ takes the grant over the bridge and redeems it; no home code is asked for
        trail.cloudApproved = true;
        const frame = tvFrame(page);
        await expect(
          frame.locator('[data-testid="big-home"], [data-bs-focus]').first()
        ).toBeVisible({ timeout: 30_000 });
        await expect(page.getByTestId("tv-frame")).toHaveAttribute(
          "src",
          new RegExp(`^${RELAY_RE}/tv/\\?bridge=1&lang=${lang}$`)
        );
        expect(trail.redeemed).toEqual([GRANT]);
        expect(home.calls.some((c) => c === "POST /api/pairing/device")).toBe(false);
        expect(trail.bearer.some((l) => l.startsWith("GET /api/auth/me ok"))).toBe(true);
        expect(trail.bearer.every((l) => l.endsWith(" ok"))).toBe(true);
        await expect(frame.locator("main, [dir]").first()).toHaveAttribute(
          "dir",
          lang === "ar" ? "rtl" : "ltr"
        );
        await shot(page, `${platform}-${lang}-cloud-home`);

        // the grant and the poll secret are in no URL and no log
        const log = await page.evaluate(
          () =>
            (
              window as unknown as { __maktabaLauncherLog?: () => string }
            ).__maktabaLauncherLog?.() ?? ""
        );
        expect(log).toContain("signIn");
        for (const secret of [GRANT, POLL_SECRET]) {
          expect(log).not.toContain(secret);
          expect(trail.network.some((l) => l.includes(secret))).toBe(false);
          expect(trail.console.some((l) => l.includes(secret))).toBe(false);
        }
        // the home is saved by the address that answered: the next start opens it at once
        await page.reload();
        await expect(page.getByTestId("tv-frame")).toHaveAttribute(
          "src",
          new RegExp(`^${RELAY_RE}/tv/`)
        );
        expect(home.unknown).toEqual([]);
      } finally {
        await keep(page, info, home, trail);
        await context.close();
      }
    });
  }
}
