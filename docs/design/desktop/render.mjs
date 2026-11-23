// Renders the desktop mockups to PNG (story 13.09 T01): each mockups/*.html at its window's
// size (its <meta name="window" content="WxH">), English and Arabic (or the languages its
// <meta name="langs"> names: the audit sheets are English only), dark, at 1× (the 2×
// renders for high-DPI review are made with SCALE=2 and not committed). PNGs go to
// mockups/png/<name>.<lang>.png, each under 1 MB. They keep full colour (a 256-colour
// palette shifted the brass and the window controls' colours), and at 1× the largest, a
// full-window photograph, is about 0.8 MB.
//
//   node docs/design/desktop/render.mjs                 render every mockup
//   RENDER_ONLY=tray,dialog node docs/design/desktop/render.mjs
//   node docs/design/desktop/render.mjs --check         only check the committed PNGs
//
// Playwright: run with NODE_PATH pointing at a node_modules that holds @playwright/test
// (web/packages/website's, after `npm ci` there). The pages are served from the repository
// root over a local HTTP server on 127.0.0.1 (the fonts need an origin), closed at the end.
// A page fails the run when a request fails, when its copy misses a key in either language,
// or when anything overflows the window sideways. Every run (and --check) also checks the
// README's colour pairs (T02) and that no style uses a physical left or right (T03).

import { createServer } from "node:http";
import { mkdir, readFile, readdir, stat } from "node:fs/promises";
import { dirname, extname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..", "..", "..");
const mockDir = join(here, "mockups");
const outDir = join(mockDir, "png");
const LANGS = ["en", "ar"];
const MAX_BYTES = 1024 * 1024;
const SCALE = Number(process.env.SCALE ?? 1);

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".woff2": "font/woff2",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".svg": "image/svg+xml",
  ".webp": "image/webp",
};

async function mockups() {
  const out = [];
  for (const f of (await readdir(mockDir)).sort()) {
    if (!f.endsWith(".html")) continue;
    const html = await readFile(join(mockDir, f), "utf8");
    const m = html.match(/<meta name="window" content="(\d+)x(\d+)"/);
    if (!m) throw new Error(`${f}: no <meta name="window">`);
    const l = html.match(/<meta name="langs" content="([a-z,]+)">/);
    out.push({ name: f.replace(/\.html$/, ""), width: +m[1], height: +m[2], langs: l ? l[1].split(",") : LANGS });
  }
  return out;
}

function serve() {
  const server = createServer(async (req, res) => {
    const path = resolve(root, "." + decodeURIComponent(new URL(req.url, "http://x").pathname));
    if (path !== root && !path.startsWith(root + sep)) return res.writeHead(403).end();
    try {
      const body = await readFile(path);
      res.writeHead(200, { "content-type": TYPES[extname(path)] ?? "application/octet-stream" });
      res.end(body);
    } catch {
      res.writeHead(404).end();
    }
  });
  return new Promise((ok) => server.listen(0, "127.0.0.1", () => ok(server)));
}

async function check(list) {
  const problems = [];
  for (const { name, langs } of list) {
    for (const lang of langs) {
      const p = join(outDir, `${name}.${lang}.png`);
      try {
        const s = await stat(p);
        if (s.size > MAX_BYTES) problems.push(`${relative(root, p)}: ${s.size} bytes, over 1 MB`);
      } catch {
        problems.push(`${relative(root, p)}: missing`);
      }
    }
  }
  return problems;
}

// T02: the colour pairs the README's contrast table states, each at its minimum.
const PAIRS = [
  ["#E8ECF3", ["#0B111C", "#0E1624", "#0F1725", "#131D2E", "#18243A", "#1C2940", "#3A3020"], 4.5],
  ["#A9B3C4", ["#0B111C", "#0E1624", "#0F1725", "#131D2E", "#18243A", "#1C2940", "#3A3020"], 4.5],
  ["#E3B252", ["#0B111C", "#0E1624", "#0F1725", "#131D2E", "#18243A", "#1C2940", "#3A3020"], 4.5],
  ["#E9BE63", ["#0B111C", "#0E1624", "#0F1725", "#131D2E", "#18243A", "#1C2940", "#3A3020"], 3],
  ["#6B7A94", ["#0B111C", "#0E1624", "#0F1725", "#131D2E", "#18243A", "#1C2940"], 3],
  ["#4CC38A", ["#0B111C", "#0F1725", "#18243A"], 3],
  ["#F0707F", ["#0B111C", "#0F1725", "#18243A"], 4.5],
  ["#1A1408", ["#E3B252"], 4.5],
  ["#FFFFFF", ["#C42B1C"], 4.5],
];

function luminance(hex) {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r, g, b] = c.map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast() {
  const problems = [];
  for (const [fg, bgs, need] of PAIRS) {
    for (const bg of bgs) {
      const [a, b] = [luminance(fg), luminance(bg)].sort((x, y) => y - x);
      const ratio = (a + 0.05) / (b + 0.05);
      if (ratio < need) problems.push(`T02: ${fg} on ${bg} is ${ratio.toFixed(2)}, needs ${need}`);
    }
  }
  return problems;
}

// T03: logical properties only, in the shared stylesheet and every mockup's own styles.
const PHYSICAL =
  /(?:^|[\s;{])(?:margin|padding|border)-(?:left|right)\b|(?:^|[\s;{])(?:left|right)\s*:|text-align\s*:\s*(?:left|right)|float\s*:\s*(?:left|right)/m;

async function physical() {
  const problems = [];
  const files = [join(here, "desktop.css"), ...(await readdir(mockDir)).filter((f) => /\.(html|css)$/.test(f)).map((f) => join(mockDir, f))];
  for (const f of files) {
    const text = await readFile(f, "utf8");
    const css = f.endsWith(".css") ? text : [...text.matchAll(/<style>([\s\S]*?)<\/style>|style="([^"]*)"/g)].map((m) => m[1] ?? m[2]).join("\n");
    for (const line of css.split("\n")) if (PHYSICAL.test(line)) problems.push(`T03: ${relative(root, f)}: ${line.trim()}`);
  }
  return problems;
}

async function render(list) {
  const require = createRequire(join(process.env.NODE_PATH ?? root, "x.js"));
  const { chromium } = require("@playwright/test");
  await mkdir(outDir, { recursive: true });
  const server = await serve();
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch();
  const problems = [];
  const made = [];
  try {
    for (const { name, width, height, langs } of list) {
      for (const lang of langs) {
        const page = await browser.newPage({
          viewport: { width, height },
          colorScheme: "dark",
          reducedMotion: "reduce",
          deviceScaleFactor: SCALE,
        });
        const failed = [];
        page.on("requestfailed", (q) => failed.push(q.url()));
        page.on("response", (q) => q.status() >= 400 && failed.push(`${q.status()} ${q.url()}`));
        page.on("pageerror", (e) => failed.push(`script: ${e.message}`));
        await page.goto(`${base}/${relative(root, join(mockDir, name + ".html"))}?lang=${lang}`, {
          waitUntil: "networkidle",
        });
        await page.evaluate(() => document.fonts.ready);
        const ready = await page.evaluate(() => document.documentElement.dataset.ready === "1");
        if (!ready) failed.push("the kit did not apply the copy");
        const wide = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
        if (wide) failed.push("overflows the window sideways");
        if (failed.length) problems.push(`${name}.${lang}: ${failed.join("; ")}`);
        const file = `${name}.${lang}${SCALE === 1 ? "" : `@${SCALE}x`}.png`;
        await page.screenshot({ path: join(outDir, file) });
        made.push(file);
        await page.close();
      }
    }
  } finally {
    await browser.close();
    server.close();
  }
  return problems;
}

const all = await mockups();
const only = process.env.RENDER_ONLY?.split(",");
const problems = [
  ...(process.argv.includes("--check") ? [] : await render(all.filter((m) => !only || only.includes(m.name)))),
  ...(await check(all)),
  ...contrast(),
  ...(await physical()),
];
if (problems.length) {
  console.error(problems.join("\n"));
  process.exit(1);
}
console.log(`${all.length} pages, ${all.reduce((n, m) => n + m.langs.length, 0)} PNGs: ok`);
