// Story 60.14: renders the store gallery mockups (make.mjs) to png/, and checks every
// captioned shot as the full set will be checked:
// - the caption's text takes at most 20% of the image (Google Play's limit for taglines);
// - the headline fits in 2 lines and the line under it in 2;
// - every picture and both faces loaded.
// Each PNG is reduced to a 256-colour palette (../quantize.py) and must stay under 1 MB with
// no metadata.
//
//   node design/store-gallery/render.mjs            render, then check
//   node design/store-gallery/render.mjs --check    only check the committed PNGs
//
// The pages are served from the repository's root on 127.0.0.1 (a free port), closed with
// the browser when the run ends.

import { spawnSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdir, readFile, readdir, rm, stat } from "node:fs/promises";
import { extname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { LANGS, SHEETS, FULL, SHEET_WIDTH, here } from "./make.mjs";
import { pngChunks } from "../png.mjs";

const repo = resolve(here, "..", "..", "..", "..", "..");
const outDir = join(here, "png");
export const MAX_BYTES = 1024 * 1024;
export const MAX_CAPTION_SHARE = 0.2;

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".woff2": "font/woff2",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".webp": "image/webp",
  ".svg": "image/svg+xml",
};

/** Every PNG the run leaves. */
export function expectedPngs() {
  const out = [];
  for (const lang of LANGS) {
    for (const n of [...Object.keys(SHEETS), ...Object.keys(FULL)]) out.push(`${n}.${lang}.png`);
  }
  return out.sort();
}

function serve() {
  const server = createServer(async (req, res) => {
    const path = resolve(repo, "." + decodeURIComponent(new URL(req.url, "http://x").pathname));
    if (path !== repo && !path.startsWith(repo + sep)) return res.writeHead(403).end();
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

export async function checkPngs() {
  const problems = [];
  const want = new Set(expectedPngs());
  const have = (await readdir(outDir).catch(() => [])).filter((f) => f.endsWith(".png"));
  for (const f of want) if (!have.includes(f)) problems.push(`missing ${f}`);
  for (const f of have) {
    if (!want.has(f)) problems.push(`unexpected ${f}`);
    const buf = await readFile(join(outDir, f));
    if (buf.length > MAX_BYTES)
      problems.push(`${f} is ${(buf.length / 1024).toFixed(0)} KB, over 1 MB`);
    const meta = pngChunks(buf).filter((c) => /^(tEXt|iTXt|zTXt|tIME|eXIf)$/.test(c));
    if (meta.length) problems.push(`${f} carries metadata: ${meta.join(", ")}`);
  }
  return problems;
}

/** In the page: each captioned shot's caption share and line counts. */
export function measure() {
  const lines = (el) => {
    if (!el) return 0;
    const r = document.createRange();
    r.selectNodeContents(el);
    const tops = new Set([...r.getClientRects()].map((x) => Math.round(x.top)));
    return tops.size;
  };
  const area = (el) => {
    if (!el) return 0;
    const r = document.createRange();
    r.selectNodeContents(el);
    return [...r.getClientRects()].reduce((s, x) => s + x.width * x.height, 0);
  };
  return [...document.querySelectorAll(".shot.captioned")].map((shot) => {
    const box = shot.getBoundingClientRect();
    const cap = shot.querySelector("[data-cap]");
    const h = cap.querySelector(".h");
    const sub = cap.querySelector(".sub");
    const wm = cap.querySelector(".wm");
    const text =
      area(h) +
      area(sub) +
      (wm ? wm.getBoundingClientRect().width * wm.getBoundingClientRect().height : 0);
    const screen = shot.querySelector(".screen").getBoundingClientRect();
    return {
      h: h.textContent,
      share: text / (box.width * box.height),
      hLines: lines(h),
      subLines: lines(sub),
      overlap: cap.getBoundingClientRect().bottom > screen.top + 1,
    };
  });
}

async function render() {
  const { chromium } = await import("@playwright/test");
  await rm(outDir, { recursive: true, force: true });
  await mkdir(outDir, { recursive: true });
  const server = await serve();
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch();
  const problems = [];
  try {
    const jobs = [];
    for (const lang of LANGS) {
      for (const n of Object.keys(SHEETS))
        jobs.push([n, lang, { width: SHEET_WIDTH, height: 900 }, 1, true]);
      for (const n of Object.keys(FULL)) {
        const f = FULL[n](lang);
        jobs.push([n, lang, { width: f.W, height: f.H }, f.scale ?? 1, false]);
      }
    }
    for (const [name, lang, viewport, scale, fullPage] of jobs) {
      const page = await browser.newPage({
        viewport,
        deviceScaleFactor: scale,
        colorScheme: "dark",
      });
      const failed = [];
      page.on("requestfailed", (q) => failed.push(q.url()));
      page.on("response", (q) => q.status() >= 400 && failed.push(`${q.status()} ${q.url()}`));
      const url = `${base}/${relative(repo, join(here, "mockups", `${name}.${lang}.html`))
        .split(sep)
        .join("/")}`;
      await page.goto(url, { waitUntil: "networkidle" });
      await page.evaluate(() => document.fonts.ready);
      await page
        .waitForFunction(
          () => [...document.images].every((i) => i.complete && i.naturalWidth > 0),
          null,
          {
            timeout: 15000,
          }
        )
        .catch(() => problems.push(`${name}.${lang}: pictures not drawn`));
      const fonts = await page.evaluate(() =>
        [...document.fonts].filter((f) => f.status === "loaded").map((f) => f.family)
      );
      // the text face is needed where there is body text (every page but the feature graphic)
      const faces = (await page.$(".sub, .sheet"))
        ? ["Maktaba Text", "Maktaba Display"]
        : ["Maktaba Display"];
      for (const face of faces) {
        if (!fonts.some((f) => f.includes(face)))
          problems.push(`${name}.${lang}: ${face} did not load`);
      }
      if (failed.length) problems.push(`${name}.${lang}: failed requests ${failed.join(", ")}`);
      for (const m of await page.evaluate(measure)) {
        const at = `${name}.${lang} "${m.h}"`;
        if (m.share > MAX_CAPTION_SHARE)
          problems.push(
            `${at}: caption takes ${(m.share * 100).toFixed(1)}% of the image, over 20%`
          );
        if (m.hLines > 2) problems.push(`${at}: headline in ${m.hLines} lines, over 2`);
        if (m.subLines > 2)
          problems.push(`${at}: the line under it in ${m.subLines} lines, over 2`);
        if (m.overlap) problems.push(`${at}: caption runs into the screen`);
      }
      await page.screenshot({ path: join(outDir, `${name}.${lang}.png`), fullPage });
      await page.close();
    }
  } finally {
    await browser.close();
    server.close();
  }
  const q = spawnSync(
    "uvx",
    ["--with", "pillow", "python", "-I", join(here, "..", "quantize.py"), outDir],
    {
      stdio: "inherit",
    }
  );
  if (q.status !== 0) problems.push("quantize.py failed");
  return problems;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const problems = process.argv.includes("--check")
    ? await checkPngs()
    : [...(await render()), ...(await checkPngs())];
  if (problems.length) {
    console.error(problems.join("\n"));
    process.exit(1);
  }
  console.log(`${expectedPngs().length} PNGs, each under 1 MB, every caption within 20%`);
}
