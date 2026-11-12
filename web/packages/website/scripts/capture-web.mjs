#!/usr/bin/env node
// The web app's product screenshots (story 60.09): the Player web app served by Vite from
// `web/`, every `/api/*` request answered from the demo library (tests/fixtures/demo-library,
// `buildRoutes`), photographed by Playwright's Chromium at 1440×900, device scale 2, dark, in
// English and Arabic, with the clock pinned to the library's. No home, no database, no account.
//
//   node scripts/capture-web.mjs [--out DIR] [--only home,film]
//
// Writes DIR/web-<screen>-<lang>.png (and DIR/webphone-home-<lang>.png, the home at a phone's
// width) (default DIR: shots-capture/web, gitignored) and
// DIR/web-capture.log: Vite's output, each page's console errors, and every /api request the
// table could not answer. CI runs it in _website.yml and uploads DIR as `product-shots-web`.
// Needs `pnpm -C web install` (Vite) and this package's `npm ci` (Playwright and Chromium).

import { spawn } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync, appendFileSync, existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";
import {
  buildRoutes,
  fillTimes,
  loadLibrary,
  LANGS,
  ROOT as FIXTURE,
} from "../../../../tests/fixtures/demo-library/routes.mjs";

const HERE = dirname(fileURLToPath(import.meta.url));
const WEB = resolve(HERE, "../../..");

/** The screens: what to open, and what must be on the page before the picture is taken. */
export const SCREENS = [
  { name: "home", path: "/", ready: "img" },
  { name: "film", path: "/videos/v-sintel", ready: "img" },
  { name: "series", path: "/series/s-open-shorts", ready: "img" },
  {
    name: "search",
    path: (lib, lang) => `/search?q=${encodeURIComponent(lib.search.query[lang])}`,
    ready: "mark",
  },
  { name: "guide", path: "/guide", ready: null },
  // the same home on a phone's width (393 × 852, the size of a current iPhone's screen), for the
  // home page's phone frame: the web app in a phone's browser, the screen its caption names
  {
    name: "home",
    app: "webphone",
    path: "/",
    ready: "img",
    viewport: { width: 393, height: 852 },
  },
];

const arg = (name, fallback) => {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : fallback;
};

/** `{id}` in a table path matches one segment. */
function matcher(routes) {
  const exact = new Map();
  const patterns = [];
  for (const r of routes) {
    if (r.path.includes("{")) {
      const re = new RegExp(`^${r.path.replace(/\{[^}]+\}/g, "[^/]+")}$`);
      patterns.push([r.method, re, r]);
    } else exact.set(`${r.method} ${r.path}`, r);
  }
  return (method, path) =>
    exact.get(`${method} ${path}`) ??
    patterns.find(([m, re]) => m === method && re.test(path))?.[2];
}

// what every page asks besides the library: the home's features (all on) and nothing to update
const EXTRA = [
  {
    method: "GET",
    path: "/api/capabilities",
    status: 200,
    body: {
      etag: "c-demo",
      features: Object.fromEntries(
        [
          "live_tv",
          "transcripts",
          "subtitles",
          "semantic_search",
          "chapters",
          "youtube",
          "plex",
          "remote_access",
          "recommendations",
        ].map((f) => [f, "on"])
      ),
    },
  },
  // nothing queued, and no recovery pass for a viewer
  { method: "GET", path: "/api/me/play-queues/current", status: 204 },
  { method: "POST", path: "/api/auth/recovery-pass", status: 204 },
];

function viewer(body) {
  const out = { ...body, is_admin: false };
  if (out.user) out.user = { ...out.user, is_admin: false };
  return out;
}

async function waitForServer(url, ms) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try {
      const r = await fetch(url);
      if (r.ok) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`Vite did not answer ${url} in ${ms / 1000}s`);
}

export async function capture({ out, only, log }) {
  const lib = loadLibrary();
  const port = 5300 + (process.pid % 500);
  const vite = spawn(
    join(WEB, "node_modules/.bin/vite"),
    ["--port", String(port), "--strictPort", "--host", "127.0.0.1"],
    {
      cwd: WEB,
      stdio: ["ignore", "pipe", "pipe"],
      env: { ...process.env, BROWSER: "none" },
    }
  );
  writeFileSync(join(out, "vite.pid"), String(vite.pid));
  vite.stdout.on("data", (d) => appendFileSync(log, `[vite] ${d}`));
  vite.stderr.on("data", (d) => appendFileSync(log, `[vite] ${d}`));
  const written = [];
  let browser;
  try {
    const base = `http://127.0.0.1:${port}`;
    await waitForServer(base, 90_000);
    browser = await chromium.launch();
    for (const lang of LANGS) {
      // the web is shown as a household member sees it: a viewer, without the admin menu
      const table = [...buildRoutes(lang, lib), ...EXTRA].map((r) =>
        r.path === "/api/auth/me" || r.path === "/api/me" ? { ...r, body: viewer(r.body) } : r
      );
      const find = matcher(table);
      const context = await browser.newContext({
        viewport: { width: 1440, height: 900 },
        deviceScaleFactor: 2,
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
          // the one-time crash-report question after sign-in, answered
          localStorage.setItem("mkt:crash-reports", "off");
        },
        { lang, at: lib.clock }
      );
      await context.route(
        (url) => url.pathname.startsWith("/api/"),
        async (route) => {
          const req = route.request();
          const path = new URL(req.url()).pathname;
          const r = find(req.method(), path);
          if (!r) {
            appendFileSync(log, `[${lang}] unanswered ${req.method()} ${path}\n`);
            return route.fulfill({
              status: 404,
              contentType: "application/json",
              body: JSON.stringify({ error: "not in the demo library" }),
            });
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
      // no socket reaches a home
      await context.route(
        (url) => url.pathname.startsWith("/ws"),
        (route) => route.abort()
      );
      for (const screen of SCREENS) {
        const id = `${screen.app ?? "web"}-${screen.name}`;
        if (only && !only.includes(screen.name) && !only.includes(id)) continue;
        const page = await context.newPage();
        if (screen.viewport) await page.setViewportSize(screen.viewport);
        await page.clock.setFixedTime(new Date(lib.clock));
        page.on(
          "console",
          (m) =>
            m.type() === "error" && appendFileSync(log, `[${lang} ${id}] console: ${m.text()}\n`)
        );
        page.on("pageerror", (e) =>
          appendFileSync(log, `[${lang} ${id}] page error: ${e.message}\n`)
        );
        const path = typeof screen.path === "function" ? screen.path(lib, lang) : screen.path;
        await page.goto(base + path, { waitUntil: "networkidle" });
        if (screen.ready) await page.waitForSelector(screen.ready, { timeout: 20_000 });
        // every image decoded, the fonts in, nothing moving
        // a lazy image below the fold never loads by itself (on a phone's width most do not), so
        // each loads now; one that has not loaded in 15 s fails the capture rather than wait
        const late = await page.evaluate(async () => {
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
          return (await Promise.race([Promise.all(waits), timeout])) === "timeout"
            ? [...document.images].filter((i) => !i.complete).map((i) => i.src)
            : [];
        });
        if (late.length)
          throw new Error(`${id} (${lang}): images never loaded: ${late.slice(0, 5).join(", ")}`);
        await page.addStyleTag({
          content:
            "*,*::before,*::after{animation:none!important;transition:none!important;caret-color:transparent!important}",
        });
        await page.waitForTimeout(300);
        const dir = await page.evaluate(
          () => document.documentElement.dir || getComputedStyle(document.body).direction
        );
        if (lang === "ar" && dir !== "rtl")
          throw new Error(`${screen.name} (ar) is not right to left: dir=${dir}`);
        const file = join(out, `${id}-${lang}.png`);
        await page.screenshot({ path: file });
        written.push(file);
        await page.close();
      }
      await context.close();
    }
  } finally {
    await browser?.close();
    vite.kill("SIGTERM");
  }
  return written;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const out = resolve(arg("--out", join(HERE, "..", "shots-capture", "web")));
  mkdirSync(out, { recursive: true });
  const log = join(out, "web-capture.log");
  writeFileSync(log, "");
  const only = arg("--only", "")?.split(",").filter(Boolean);
  if (!existsSync(join(WEB, "node_modules/.bin/vite"))) {
    console.error("capture-web: run `pnpm -C web install` first (Vite)");
    process.exit(1);
  }
  capture({ out, only: only.length ? only : null, log }).then(
    (files) => console.log(`capture-web: ${files.length} images in ${out} (log: ${log})`),
    (e) => {
      console.error(`capture-web: ${e.message} (log: ${log})`);
      process.exit(1);
    }
  );
}
