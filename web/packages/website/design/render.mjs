// Renders the part A mockups to PNG (story 60.01 T01): every mockup at 360, 768 and 1280 px,
// English and Arabic, dark; the text pages (front door, legal) and the component sheet in
// light too. Full-page PNGs under mockups/png/, each under 1 MB. Each page is also loaded
// at 320 px and fails the run if it scrolls sideways (WCAG 1.4.10 reflow).
//
//   node design/render.mjs            render, then check sizes
//   RENDER_ONLY=a,b node design/render.mjs   render only those mockups
//   node design/render.mjs --check    only check the committed PNGs (count, size, no metadata)
//
// The pages are served over a local HTTP server rooted at web/ (fonts need an origin, and
// the mockups reach web/public/icons for the app icon). The server listens on 127.0.0.1 on
// a free port and is closed when the run ends. The PNGs are then reduced to a 256-colour
// palette (quantize.py, Pillow through uvx).

import { spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, readFile, readdir, rm, stat } from "node:fs/promises";
import { dirname, extname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { LANGS, MOCKUPS } from "./mockups/make.mjs";
import { pngChunks } from "./png.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..", "..", "..");
const outDir = join(here, "mockups", "png");

export const WIDTHS = [360, 768, 1280];
export const LIGHT = new Set(["front-door", "legal", "components"]);
export const MAX_BYTES = 1024 * 1024;

/** Every PNG the run must leave, as file names. */
export function expectedPngs() {
  const out = [];
  for (const name of Object.keys(MOCKUPS)) {
    for (const lang of LANGS) {
      for (const width of WIDTHS) {
        out.push(`${name}.${lang}.${width}.dark.png`);
        if (LIGHT.has(name)) out.push(`${name}.${lang}.${width}.light.png`);
      }
    }
  }
  return out;
}

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".woff2": "font/woff2",
  ".png": "image/png",
};

function serve() {
  const server = createServer(async (req, res) => {
    const path = resolve(webRoot, "." + decodeURIComponent(new URL(req.url, "http://x").pathname));
    if (path !== webRoot && !path.startsWith(webRoot + sep)) {
      res.writeHead(403).end();
      return;
    }
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

/** Count, size and metadata of the PNGs in mockups/png/. */
export async function checkPngs() {
  const problems = [];
  const want = new Set(expectedPngs());
  const have = (await readdir(outDir).catch(() => [])).filter((f) => f.endsWith(".png"));
  for (const f of want) if (!have.includes(f)) problems.push(`missing ${f}`);
  for (const f of have) {
    if (!want.has(f)) problems.push(`unexpected ${f}`);
    const path = join(outDir, f);
    const { size } = await stat(path);
    if (size > MAX_BYTES) problems.push(`${f} is ${(size / 1024).toFixed(0)} KB, over 1 MB`);
    const meta = pngChunks(await readFile(path)).filter((c) =>
      /^(tEXt|iTXt|zTXt|tIME|eXIf)$/.test(c)
    );
    if (meta.length) problems.push(`${f} carries metadata: ${meta.join(", ")}`);
  }
  return problems;
}

async function render() {
  const { chromium } = await import("@playwright/test");
  // RENDER_ONLY=front-door,404 renders just those while iterating (the size check then
  // still reads every committed PNG).
  const only = process.env.RENDER_ONLY?.split(",");
  if (!only) await rm(outDir, { recursive: true, force: true });
  await mkdir(outDir, { recursive: true });
  const server = await serve();
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch();
  const problems = [];
  try {
    for (const name of Object.keys(MOCKUPS).filter((n) => !only || only.includes(n))) {
      for (const lang of LANGS) {
        const url = `${base}/${relative(webRoot, join(here, "mockups", `${name}.${lang}.html`))}`;
        const schemes = LIGHT.has(name) ? ["dark", "light"] : ["dark"];
        for (const scheme of schemes) {
          for (const width of [320, ...WIDTHS]) {
            const page = await browser.newPage({
              viewport: { width, height: 800 },
              colorScheme: scheme,
              reducedMotion: "reduce",
              deviceScaleFactor: 1,
            });
            const failed = [];
            page.on("requestfailed", (r) => failed.push(r.url()));
            page.on(
              "response",
              (r) => r.status() >= 400 && failed.push(`${r.status()} ${r.url()}`)
            );
            await page.goto(url, { waitUntil: "networkidle" });
            await page.evaluate(() => document.fonts.ready);
            const fonts = await page.evaluate(() =>
              [...document.fonts].filter((f) => f.status === "loaded").map((f) => f.family)
            );
            if (!fonts.some((f) => f.includes("Maktaba Text"))) {
              problems.push(`${name}.${lang}: the site's font did not load`);
            }
            if (failed.length)
              problems.push(`${name}.${lang}: failed requests ${failed.join(", ")}`);
            const overflow = await page.evaluate(
              () => document.documentElement.scrollWidth - document.documentElement.clientWidth
            );
            if (overflow > 0)
              problems.push(`${name}.${lang} at ${width}px scrolls sideways by ${overflow}px`);
            if (width !== 320) {
              await page.screenshot({
                path: join(outDir, `${name}.${lang}.${width}.${scheme}.png`),
                fullPage: true,
                animations: "disabled",
              });
            }
            await page.close();
          }
        }
      }
    }
  } finally {
    await browser.close();
    server.close();
  }
  // A 256-colour palette: the renders are flat colour and text (quantize.py).
  const q = spawnSync(
    "uvx",
    ["--with", "pillow", "python", "-I", join(here, "quantize.py"), outDir],
    {
      encoding: "utf8",
    }
  );
  if (q.status !== 0) problems.push(`quantize.py failed: ${q.error?.message ?? q.stderr}`);
  return problems;
}

async function main(argv) {
  const problems = argv.includes("--check") ? [] : await render();
  problems.push(...(await checkPngs()));
  if (problems.length) {
    console.error(`render: ${problems.length} problem(s):\n  ${problems.join("\n  ")}`);
    process.exit(1);
  }
  const files = expectedPngs();
  let total = 0;
  for (const f of files) total += (await stat(join(outDir, f))).size;
  console.log(
    `render: ${files.length} PNGs, ${(total / 1024 / 1024).toFixed(1)} MB in all, each under 1 MB`
  );
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
