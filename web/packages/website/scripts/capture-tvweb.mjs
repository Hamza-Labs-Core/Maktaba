#!/usr/bin/env node
// The Samsung and LG store screens (story 60.14 part 2): both launchers (apps/tv/web) open the
// home's TV web UI, the big screen at /tv/ (web/src/tv, web/src/bigscreen), so their gallery is
// that UI on the demo library, as capture-web.mjs takes the web app: Vite serves it from `web/`,
// every `/api/*` request is answered from the demo library (tests/fixtures/demo-library,
// `buildRoutes`), the clock is pinned to the library's, and Playwright's Chromium takes it at
// 1920×1080, dark, at device scale 1 (a TV's screen). The sign-in code is the launcher's own
// first screen (the cloud code, maktaba.tv/tv), built as the release builds the packages
// (tv-web-release.yml: `VERSION=X.Y.Z pnpm run build:launcher`, which stamps the version the
// screen shows) and served from web/dist-launcher by `vite preview`. The version is the next
// release's, as the release session computes its tag (tools/release/next_tag.py's next_patch:
// the highest vX.Y.Z tag, here and on the remote, plus one patch), or --version X.Y.Z.
//
//   node scripts/capture-tvweb.mjs [--out DIR] [--only home,player] [--version X.Y.Z]
//
// Writes DIR/<store>-<screen>-<lang>.png, the names the store gallery's compose.mjs reads
// (design/store-gallery/beats.json: Samsung and LG show English screens with the Arabic home
// second), one capture written under every name that shows it, and DIR/tvweb-capture.log
// (Vite's and the launcher build's output, each page's console errors, every /api request the
// table could not answer).
// CI runs it in _website.yml's shots job and uploads DIR as `store-shots-tvweb`.
//
// What the library lacks is made here, from it: the player's stream is a still of Curiosity:
// Landing on Mars (the library's backdrop, public domain) as a WebM of the film's length, made
// with Playwright's own ffmpeg, and its subtitles are the lines Apple's demo player shows
// (apps/apple/UITests/Fixtures/Stream/demo-subs-*.vtt), Arabic first; the launcher's cloud
// code is answered as Maktaba Cloud answers it. No home, no database, no account.
// Needs `pnpm -C web install` (Vite) and this package's `npm ci` (Playwright and Chromium).

import { spawn, spawnSync } from "node:child_process";
import {
  appendFileSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";
import {
  buildRoutes,
  fillTimes,
  loadLibrary,
  ROOT as FIXTURE,
} from "../../../../tests/fixtures/demo-library/routes.mjs";
import { EXTRA, matcher, viewer, waitForServer } from "./capture-web.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const WEB = resolve(HERE, "../../..");
const REPO = resolve(WEB, "..");
const SUBS = join(REPO, "apps/apple/UITests/Fixtures/Stream");
/** The film the player shows, and its art. */
const FILM = "v-curiosity";

/**
 * The screens: where each is, what must be on it before the picture, and the names it is
 * written under (`<store>-<screen>-<lang>`, as beats.json names the store's capture).
 */
export const SCREENS = [
  {
    name: "home",
    path: "/tv/big",
    ready: '[data-testid="big-hero"] img',
    out: {
      en: ["samsung-home-en", "lg-home-en"],
      ar: ["samsung-home-ar", "lg-home-ar"],
    },
  },
  {
    name: "series",
    path: "/tv/big/series/s-caminandes",
    ready: "img",
    out: { en: ["lg-series-en"] },
  },
  {
    name: "player",
    path: `/tv/big/watch/${FILM}`,
    ready: "video",
    player: true,
    out: { en: ["samsung-player-en", "lg-subtitles-en"] },
  },
  {
    name: "guide",
    path: "/tv/big/live",
    ready: '[data-testid="big-live"] img',
    out: { en: ["samsung-guide-en", "lg-guide-en"] },
  },
  {
    name: "code",
    launcher: true,
    ready: '[data-testid="cloud-code"]',
    out: { en: ["lg-code-en"] },
  },
];

const arg = (name, fallback) => {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : fallback;
};

/**
 * Playwright's own ffmpeg (VP9 and a JPEG pipe are all it takes), else one on the PATH; with
 * neither, Playwright installs its own (the pinned version's) and that is used.
 */
export function findFfmpeg(install = true) {
  const roots = [
    process.env.PLAYWRIGHT_BROWSERS_PATH,
    "/ms-playwright",
    join(homedir(), ".cache/ms-playwright"),
    join(homedir(), "Library/Caches/ms-playwright"),
  ].filter(Boolean);
  for (const root of roots) {
    if (!existsSync(root)) continue;
    const dirs = readdirSync(root)
      .filter((d) => /^ffmpeg-\d+$/.test(d))
      .sort((a, b) => Number(b.slice(7)) - Number(a.slice(7)));
    for (const d of dirs)
      for (const f of ["ffmpeg-linux", "ffmpeg-mac", "ffmpeg-win64.exe"])
        if (existsSync(join(root, d, f))) return join(root, d, f);
  }
  const which = spawnSync("sh", ["-c", "command -v ffmpeg"], { encoding: "utf8" });
  if (which.status === 0 && which.stdout.trim()) return which.stdout.trim();
  if (install) {
    spawnSync(join(HERE, "..", "node_modules/.bin/playwright"), ["install", "ffmpeg"], {
      stdio: "inherit",
    });
    return findFfmpeg(false);
  }
  throw new Error("no ffmpeg: install Playwright's (npx playwright install ffmpeg)");
}

/** The highest vX.Y.Z of `tags` plus one patch, as next_tag.py's next_patch counts it. */
export function nextPatch(tags) {
  const versions = tags
    .map((t) => /^v(\d+)\.(\d+)\.(\d+)$/.exec(t.trim()))
    .filter(Boolean)
    .map((m) => [Number(m[1]), Number(m[2]), Number(m[3])]);
  if (!versions.length) throw new Error("no vX.Y.Z tag to count from");
  versions.sort((a, b) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2]);
  const [x, y, z] = versions.at(-1);
  return `${x}.${y}.${z + 1}`;
}

/**
 * The v* tags: this checkout's and the remote's (CI's checkout is shallow and has none; GitHub's
 * own address in CI, origin's otherwise). The remote is asked from outside the checkout, whose
 * ownership a container's git may refuse.
 */
export function releaseTags(log) {
  const git = (args, cwd) => {
    const r = spawnSync("git", args, { cwd, encoding: "utf8" });
    if (r.status !== 0) {
      appendFileSync(log, `[version] git ${args.join(" ")}: ${r.stderr || r.error?.message}\n`);
      return "";
    }
    return r.stdout;
  };
  const tags = git(["tag", "-l", "v*"], REPO).split("\n");
  const remote =
    process.env.GITHUB_REPOSITORY && process.env.GITHUB_SERVER_URL
      ? `${process.env.GITHUB_SERVER_URL}/${process.env.GITHUB_REPOSITORY}.git`
      : git(["remote", "get-url", "origin"], REPO).trim();
  if (remote)
    for (const line of git(["ls-remote", "--tags", "--refs", remote, "v*"], tmpdir()).split("\n"))
      if (line.includes("refs/tags/")) tags.push(line.slice(line.indexOf("refs/tags/") + 10));
  return tags.filter(Boolean);
}

/** The version the launcher is built at: `given` (--version), else the next release's. */
export function launcherVersion(given, tags) {
  const version = given ?? nextPatch(tags());
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version))
    throw new Error(`not a release version (X.Y.Z): ${version}`);
  return version;
}

/** The launcher as tv-web-release.yml builds it: `pnpm run build:launcher` at VERSION. */
function buildLauncher(version, log) {
  appendFileSync(log, `[launcher] build:launcher at ${version}\n`);
  const r = spawnSync("pnpm", ["run", "build:launcher"], {
    cwd: WEB,
    env: { ...process.env, VERSION: version },
    encoding: "utf8",
    maxBuffer: 1 << 26,
  });
  appendFileSync(log, `[launcher] ${r.stdout ?? ""}${r.stderr ?? ""}\n`);
  if (r.status !== 0)
    throw new Error(`build:launcher failed (${r.status ?? r.error?.message}); see the log`);
  const assets = join(WEB, "dist-launcher", "assets");
  const stamped = readdirSync(assets)
    .filter((f) => f.endsWith(".js"))
    .some((f) => readFileSync(join(assets, f), "utf8").includes(JSON.stringify(version)));
  if (!stamped) throw new Error(`the launcher build does not carry its version ${version}`);
}

/** A WebM of `seconds` of one still (a frame a second, each after the first a repeat). */
export function stillVideo(jpeg, seconds, file) {
  const frame = readFileSync(jpeg);
  const r = spawnSync(
    findFfmpeg(),
    [
      ...["-hide_banner", "-loglevel", "error", "-y"],
      ...["-f", "image2pipe", "-framerate", "1", "-c:v", "mjpeg", "-i", "pipe:0"],
      ...["-vf", "scale=1280:720", "-c:v", "libvpx-vp9", "-deadline", "realtime"],
      ...["-cpu-used", "8", "-crf", "30", "-b:v", "0", "-g", "100000", file],
    ],
    { input: Buffer.concat(Array.from({ length: seconds }, () => frame)), maxBuffer: 1 << 26 }
  );
  if (r.status !== 0) throw new Error(`ffmpeg: ${r.stderr?.toString() || r.error?.message}`);
  return file;
}

/** Vite from `web/`, with `args`; its output goes to the log. */
function vite(args, port, log, tag) {
  const p = spawn(
    join(WEB, "node_modules/.bin/vite"),
    [...args, "--port", String(port), "--strictPort", "--host", "127.0.0.1"],
    { cwd: WEB, stdio: ["ignore", "pipe", "pipe"], env: { ...process.env, BROWSER: "none" } }
  );
  p.stdout.on("data", (d) => appendFileSync(log, `[${tag}] ${d}`));
  p.stderr.on("data", (d) => appendFileSync(log, `[${tag}] ${d}`));
  return p;
}

/** Every image decoded and the fonts in (a lazy image is made eager), or the names of those not. */
async function settle(page) {
  return page.evaluate(async () => {
    await document.fonts.ready;
    for (const i of document.images) i.loading = "eager";
    const waits = [...document.images].map((i) =>
      i.complete
        ? null
        : new Promise((r) => {
            i.onload = i.onerror = r;
          })
    );
    const timeout = new Promise((r) => setTimeout(() => r("timeout"), 15_000));
    const late =
      (await Promise.race([Promise.all(waits), timeout])) === "timeout"
        ? [...document.images].filter((i) => !i.complete).map((i) => i.src)
        : [];
    const broken = [...document.images]
      .filter((i) => i.complete && i.naturalWidth === 0 && i.getAttribute("src"))
      .map((i) => i.src);
    return [...late, ...broken];
  });
}

/** The launcher's TV: an LG (webOS) set, as the launcher's own Playwright run makes one. */
function webosShim() {
  const w = window;
  w.webOS = {
    service: {
      request: (_uri, req) =>
        setTimeout(() => req.onSuccess({ wired: { state: "connected", ipAddress: "10.0.0.5" } })),
    },
    deviceInfo: (cb) => cb({ modelName: "OLED55CX", uhd: true }),
    platformBack: () => {},
  };
}

export async function capture({ out, only, log, version }) {
  const lib = loadLibrary();
  const film = lib.videos.find((v) => v.id === FILM);
  const scratch = mkdtempSync(join(tmpdir(), "maktaba-tvweb-"));
  const port = 5300 + (process.pid % 400);
  const launcherPort = port + 400;
  const procs = [];
  const written = [];
  let browser;
  try {
    const stream = stillVideo(
      join(FIXTURE, "art", `${film.art}-backdrop.jpg`),
      film.duration_sec,
      join(scratch, "film.webm")
    );
    const launcher = !only || only.includes("code");
    if (launcher) buildLauncher(version, log);
    procs.push(vite([], port, log, "vite"));
    if (launcher)
      procs.push(
        vite(["preview", "--config", "vite.launcher.config.ts"], launcherPort, log, "vite-launcher")
      );
    writeFileSync(join(out, "vite.pid"), procs.map((p) => p.pid).join("\n"));
    const base = `http://127.0.0.1:${port}`;
    const launcherBase = `http://127.0.0.1:${launcherPort}`;
    await waitForServer(base, 90_000);
    if (launcher) await waitForServer(launcherBase, 90_000);
    // /tv/ is the TV build's page (index.tv.html), which Vite serves transformed at its own path
    const tvPage = await (await fetch(`${base}/index.tv.html`)).text();
    browser = await chromium.launch();
    for (const lang of ["en", "ar"]) {
      const screens = SCREENS.filter((s) => s.out[lang] && (!only || only.includes(s.name)));
      if (!screens.length) continue;
      const find = matcher(
        [...buildRoutes(lang, lib), ...EXTRA].map((r) =>
          r.path === "/api/auth/me" || r.path === "/api/me" ? { ...r, body: viewer(r.body) } : r
        )
      );
      const context = await browser.newContext({
        viewport: { width: 1920, height: 1080 },
        deviceScaleFactor: 1,
        colorScheme: "dark",
        locale: lang === "ar" ? "ar" : "en-GB",
        timezoneId: "UTC",
        serviceWorkers: "block",
      });
      await context.addInitScript(
        ({ lang, at }) => {
          localStorage.setItem("mkt:locale", lang);
          localStorage.setItem("mkt:theme", "dark");
          localStorage.setItem("mkt:usage:consent", JSON.stringify({ granted: false, at }));
          localStorage.setItem("mkt:crash-reports", "off");
          // the big screen's subtitles on: the first track (Arabic) shows
          localStorage.setItem("mkt:big:prefs", JSON.stringify({ subtitlesOn: true }));
        },
        { lang, at: lib.clock }
      );
      // the TV build's page for every /tv/ address
      await context.route(
        (url) => url.origin === base && url.pathname.startsWith("/tv"),
        (route) =>
          route.request().resourceType() === "document"
            ? route.fulfill({ status: 200, contentType: "text/html", body: tvPage })
            : route.fallback()
      );
      // the player's stream and its subtitles
      await context.route(
        (url) => url.pathname.startsWith("/demo-stream/"),
        (route) => {
          const name = new URL(route.request().url()).pathname.slice("/demo-stream/".length);
          if (name === "film.webm")
            return route.fulfill({
              status: 200,
              contentType: "video/webm",
              body: readFileSync(stream),
            });
          const sub = /^subs-(en|ar)\.vtt$/.exec(name);
          if (sub)
            return route.fulfill({
              status: 200,
              contentType: "text/vtt",
              body: readFileSync(join(SUBS, `demo-subs-${sub[1]}.vtt`), "utf8").replace(
                /^X-TIMESTAMP-MAP=.*\n/m,
                ""
              ),
            });
          return route.fulfill({ status: 404 });
        }
      );
      await context.route(
        (url) => url.origin === base && url.pathname.startsWith("/api/"),
        async (route) => {
          const req = route.request();
          const path = new URL(req.url()).pathname;
          const json = (status, body) =>
            route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
          // the stream session (direct play of the still), its heartbeat and its end
          if (path === "/api/stream/sessions" && req.method() === "POST")
            return json(201, {
              session_id: "s-demo",
              mode: "direct",
              direct_url: "/demo-stream/film.webm",
              duration_sec: film.duration_sec,
              expires_at: fillTimes("{{now+3600}}", lib.clock),
              // the Arabic line alone: with a second track, headless Chromium showed both
              subtitles: [{ lang: "ar", label: "العربية", url: "/demo-stream/subs-ar.vtt" }],
            });
          if (path.startsWith("/api/stream/sessions/")) return route.fulfill({ status: 204 });
          // a household member with no limit (Epic 57's answer for an unlimited account)
          if (path === "/api/me/parental") return json(200, { restricted: false });
          if (path === "/api/parental/children") return json(200, { children: [] });
          const r = find(req.method(), path);
          if (!r) {
            appendFileSync(log, `[${lang}] unanswered ${req.method()} ${path}\n`);
            return json(404, { error: "not in the demo library" });
          }
          if (r.art)
            return route.fulfill({
              status: 200,
              contentType: "image/jpeg",
              body: readFileSync(join(FIXTURE, "art", r.art)),
            });
          return route.fulfill({
            status: r.status,
            contentType: "application/json",
            body: r.status === 204 ? "" : fillTimes(JSON.stringify(r.body), lib.clock),
          });
        }
      );
      await context.route(
        (url) => url.origin === base && url.pathname.startsWith("/ws"),
        (route) => route.abort()
      );
      // the launcher's first screen asks Maktaba Cloud for a code (story 58.03), answered as the
      // cloud does; nobody approves it
      await context.route("https://api.maktaba.tv/v1/tv/**", (route) => {
        const req = route.request();
        const origin = req.headers().origin ?? "*";
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
        const path = new URL(req.url()).pathname;
        const body = (status, b) =>
          route.fulfill({
            status,
            contentType: "application/json",
            headers: cors,
            body: JSON.stringify(b),
          });
        if (path === "/v1/tv/codes")
          return body(201, {
            code: "ABCD-2345",
            poll_secret: "demo-poll",
            expires_in: 600,
            interval: 5,
            verify_url: "https://maktaba.tv/tv?c=ABCD2345",
          });
        if (path === "/v1/tv/codes/poll") return body(202, { status: "pending", interval: 5 });
        return body(404, { error: "not_found" });
      });

      for (const screen of screens) {
        const page = await context.newPage();
        await page.clock.setFixedTime(new Date(lib.clock));
        page.on(
          "console",
          (m) =>
            m.type() === "error" &&
            appendFileSync(log, `[${lang} ${screen.name}] console: ${m.text()}\n`)
        );
        page.on("pageerror", (e) =>
          appendFileSync(log, `[${lang} ${screen.name}] page error: ${e.message}\n`)
        );
        if (screen.launcher) {
          await page.addInitScript(webosShim);
          await page.goto(`${launcherBase}/`, { waitUntil: "networkidle" });
        } else {
          // a playing stream never leaves the network idle
          await page.goto(base + screen.path, {
            waitUntil: screen.player ? "load" : "networkidle",
          });
        }
        await page.waitForSelector(screen.ready, { timeout: 30_000 });
        if (screen.launcher) {
          // the version the release stamps, never the unstamped build's "dev"
          const shown = await page.textContent(".ml-version");
          if (!shown?.includes(version))
            throw new Error(`the launcher shows "${shown}", not version ${version}`);
        }
        const late = await settle(page);
        if (late.length)
          throw new Error(
            `${screen.name} (${lang}): images never loaded: ${late.slice(0, 5).join(", ")}`
          );
        if (screen.player) {
          // the picture is in, the Arabic line is showing, and the controls have gone
          await page.waitForFunction(
            () => {
              const v = document.querySelector("video");
              const t = v?.textTracks?.[0];
              return (
                v &&
                v.readyState >= 2 &&
                t?.language === "ar" &&
                t.mode === "showing" &&
                (t.activeCues?.length ?? 0) > 0
              );
            },
            null,
            { timeout: 30_000 }
          );
          // the controls hide while watching (the picture is a still, so it plays on unchanged)
          await page.waitForSelector(".bs-watch--watching", { timeout: 30_000 });
        }
        await page.addStyleTag({
          content:
            "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}",
        });
        await page.waitForTimeout(500);
        const dir = await page.evaluate(
          () => document.documentElement.dir || getComputedStyle(document.body).direction
        );
        if (lang === "ar" && dir !== "rtl")
          throw new Error(`${screen.name} (ar) is not right to left: dir=${dir}`);
        const [first, ...rest] = screen.out[lang];
        const file = join(out, `${first}.png`);
        await page.screenshot({ path: file });
        written.push(file);
        for (const name of rest) {
          copyFileSync(file, join(out, `${name}.png`));
          written.push(join(out, `${name}.png`));
        }
        await page.close();
      }
      await context.close();
    }
  } finally {
    await browser?.close();
    for (const p of procs) p.kill("SIGTERM");
    rmSync(scratch, { recursive: true, force: true });
  }
  return written;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const out = resolve(arg("--out", join(HERE, "..", "shots-capture", "tvweb")));
  mkdirSync(out, { recursive: true });
  const log = join(out, "tvweb-capture.log");
  writeFileSync(log, "");
  const only = arg("--only", "")?.split(",").filter(Boolean);
  if (!existsSync(join(WEB, "node_modules/.bin/vite"))) {
    console.error("capture-tvweb: run `pnpm -C web install` first (Vite)");
    process.exit(1);
  }
  let version;
  try {
    version = launcherVersion(arg("--version", undefined), () => releaseTags(log));
  } catch (e) {
    console.error(`capture-tvweb: ${e.message} (log: ${log})`);
    process.exit(1);
  }
  capture({ out, only: only.length ? only : null, log, version }).then(
    (files) =>
      console.log(
        `capture-tvweb: ${files.length} images in ${out}, the launcher at ${version} (log: ${log})`
      ),
    (e) => {
      console.error(`capture-tvweb: ${e.message} (log: ${log})`);
      process.exit(1);
    }
  );
}
