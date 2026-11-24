// Draws the installers' art (story 13.14) from installer-art/art.html, on the real tokens,
// faces and the arch, and writes the files each installer reads, in the format and at the
// sizes its tool needs:
//
//   apps/desktop/src-tauri/windows/installer/   the -setup.exe (NSIS, a custom template):
//     brand-<lang>-<100|150|200>.bmp            the brand pane at 100, 150 and 200% (the
//                                               template picks one by the window's DPI)
//     title-<welcome|progress>-<lang>-<scale>.bmp  each page's title in Maktaba Display
//   apps/desktop/src-tauri/windows/wix/         the server app's .msi (WiX, tauri):
//     dialog.bmp 493x312, banner.bmp 493x58     WixUIDialogBmp, WixUIBannerBmp
//   apps/desktop-player/msi/                    the Player's .msi (theme_msi.py puts them in):
//     dialog-<lang>.bmp, banner-<lang>.bmp      the Arabic ones mirrored, for its ar-SA transform
//   apps/desktop/src-tauri/macos/               the .dmg's Finder background:
//     dmg-background.tiff                       660x400 with its 1320x800 @2x (tiffutil)
//   docs/design/desktop/installer-art/preview.png   every piece at 1x, for review
//
// BMPs are 24-bit, uncompressed, bottom-up (what NSIS's LoadImage and Windows Installer
// read). Renders are deterministic for one Chromium and one font set; the committed files
// are the reference, and tools/ci/tests/test_installers.py checks their formats and sizes.
//
//   NODE_PATH=<node_modules holding @playwright/test or playwright-core> \
//     node docs/design/desktop/installer-art.mjs            (macOS: tiffutil makes the .tiff)
//
// The pages are served from the repository root on 127.0.0.1 (the fonts need an origin);
// the server and the browser are closed at the end. Demo media: none (the art is the arch,
// the wordmark and type).

import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, extname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";
import { inflateSync } from "node:zlib";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..", "..", "..");
const ART = join(here, "installer-art", "art.html");
const NSIS = "apps/desktop/src-tauri/windows/installer";
const WIX = "apps/desktop/src-tauri/windows/wix";
const PLAYER = "apps/desktop-player/msi";
const MAC = "apps/desktop/src-tauri/macos";
const SCALES = { 100: 1, 150: 1.5, 200: 2 };

// every piece: its page query, its CSS size, the scales and where each render goes
function pieces() {
  const out = [];
  for (const lang of ["en", "ar"]) {
    for (const [pct, scale] of Object.entries(SCALES)) {
      out.push({ q: { piece: "nsis-brand", lang }, w: 164, h: 314, scale, file: `${NSIS}/brand-${lang}-${pct}.bmp` });
      for (const page of ["welcome", "progress"]) {
        out.push({ q: { piece: "nsis-title", page, lang }, w: 282, h: 40, scale, file: `${NSIS}/title-${page}-${lang}-${pct}.bmp` });
      }
    }
    out.push({ q: { piece: "wix-dialog", lang }, w: 493, h: 312, scale: 1, file: `${PLAYER}/dialog-${lang}.bmp` });
    out.push({ q: { piece: "wix-banner", lang }, w: 493, h: 58, scale: 1, file: `${PLAYER}/banner-${lang}.bmp` });
  }
  // tauri's .msi takes one bitmap per kind for every language: the English layout
  out.push({ q: { piece: "wix-dialog", lang: "en" }, w: 493, h: 312, scale: 1, file: `${WIX}/dialog.bmp` });
  out.push({ q: { piece: "wix-banner", lang: "en" }, w: 493, h: 58, scale: 1, file: `${WIX}/banner.bmp` });
  out.push({ q: { piece: "dmg", lang: "en" }, w: 660, h: 400, scale: 1, file: "dmg@1x.png", tmp: true });
  out.push({ q: { piece: "dmg", lang: "en" }, w: 660, h: 400, scale: 2, file: "dmg@2x.png", tmp: true });
  return out;
}

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".woff2": "font/woff2",
  ".png": "image/png",
  ".bmp": "image/bmp",
};

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

// --- PNG in, BMP out -------------------------------------------------------------------

/** Decodes an 8-bit, non-interlaced RGB or RGBA PNG (what Chromium's screenshots are). */
export function decodePng(buf) {
  if (buf.readUInt32BE(0) !== 0x89504e47) throw new Error("not a PNG");
  let off = 8;
  let width, height, type;
  const idat = [];
  while (off < buf.length) {
    const len = buf.readUInt32BE(off);
    const kind = buf.toString("latin1", off + 4, off + 8);
    const data = buf.subarray(off + 8, off + 8 + len);
    if (kind === "IHDR") {
      width = data.readUInt32BE(0);
      height = data.readUInt32BE(4);
      const depth = data[8];
      type = data[9];
      if (depth !== 8 || (type !== 2 && type !== 6) || data[12] !== 0) throw new Error(`PNG depth ${depth} type ${type}: not 8-bit RGB(A), or interlaced`);
    } else if (kind === "IDAT") idat.push(data);
    else if (kind === "IEND") break;
    off += 12 + len;
  }
  const bpp = type === 6 ? 4 : 3;
  const raw = inflateSync(Buffer.concat(idat));
  const stride = width * bpp;
  const px = Buffer.alloc(height * stride);
  for (let y = 0; y < height; y++) {
    const f = raw[y * (stride + 1)];
    const line = raw.subarray(y * (stride + 1) + 1, (y + 1) * (stride + 1));
    for (let x = 0; x < stride; x++) {
      const a = x >= bpp ? px[y * stride + x - bpp] : 0;
      const b = y > 0 ? px[(y - 1) * stride + x] : 0;
      const c = x >= bpp && y > 0 ? px[(y - 1) * stride + x - bpp] : 0;
      let v = line[x];
      if (f === 1) v += a;
      else if (f === 2) v += b;
      else if (f === 3) v += (a + b) >> 1;
      else if (f === 4) {
        const p = a + b - c;
        const pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c);
        v += pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
      } else if (f !== 0) throw new Error(`PNG filter ${f}`);
      px[y * stride + x] = v & 0xff;
    }
  }
  return { width, height, bpp, px };
}

/** A 24-bit uncompressed bottom-up BMP (BITMAPINFOHEADER) of an opaque image. */
export function encodeBmp({ width, height, bpp, px }) {
  const row = Math.ceil((width * 3) / 4) * 4;
  const size = 54 + row * height;
  const out = Buffer.alloc(size);
  out.write("BM", 0, "latin1");
  out.writeUInt32LE(size, 2);
  out.writeUInt32LE(54, 10);
  out.writeUInt32LE(40, 14);
  out.writeInt32LE(width, 18);
  out.writeInt32LE(height, 22);
  out.writeUInt16LE(1, 26);
  out.writeUInt16LE(24, 28);
  out.writeUInt32LE(0, 30); // BI_RGB
  out.writeUInt32LE(row * height, 34);
  out.writeInt32LE(2835, 38); // 72 dpi
  out.writeInt32LE(2835, 42);
  for (let y = 0; y < height; y++) {
    const dst = 54 + (height - 1 - y) * row;
    for (let x = 0; x < width; x++) {
      const s = (y * width + x) * bpp;
      if (bpp === 4 && px[s + 3] !== 255) throw new Error(`a pixel at ${x},${y} is not opaque`);
      out[dst + x * 3] = px[s + 2];
      out[dst + x * 3 + 1] = px[s + 1];
      out[dst + x * 3 + 2] = px[s];
    }
  }
  return out;
}

// --- rendering --------------------------------------------------------------------------

function playwright() {
  const require = createRequire(join(process.env.NODE_PATH ?? root, "x.js"));
  for (const name of ["@playwright/test", "playwright-core", "playwright"]) {
    try {
      return require(name);
    } catch {}
  }
  throw new Error("no Playwright: set NODE_PATH to a node_modules holding @playwright/test or playwright-core");
}

async function render() {
  const { chromium } = playwright();
  const server = await serve();
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch();
  const scratch = await mkdtemp(join(tmpdir(), "installer-art-"));
  const problems = [];
  const made = [];
  try {
    for (const p of pieces()) {
      const page = await browser.newPage({
        viewport: { width: p.w, height: p.h },
        colorScheme: "dark",
        reducedMotion: "reduce",
        deviceScaleFactor: p.scale,
      });
      const failed = [];
      page.on("requestfailed", (r) => failed.push(r.url()));
      page.on("response", (r) => r.status() >= 400 && failed.push(`${r.status()} ${r.url()}`));
      page.on("pageerror", (e) => failed.push(`script: ${e.message}`));
      const query = new URLSearchParams(p.q).toString();
      await page.goto(`${base}/${relative(root, ART)}?${query}`, { waitUntil: "networkidle" });
      await page.evaluate(() => document.fonts.ready);
      await page.waitForFunction(() => document.documentElement.dataset.fitted === "1");
      const ready = await page.evaluate(() => document.documentElement.dataset.ready === "1");
      if (!ready) failed.push("the kit did not draw the piece");
      const over = await page.evaluate(() => {
        const el = document.getElementById("piece");
        return [...el.querySelectorAll("h1, .wordmark span")].some((t) => t.scrollWidth > el.clientWidth);
      });
      if (over) failed.push("its words overflow the piece");
      if (failed.length) problems.push(`${p.file}: ${failed.join("; ")}`);
      const png = await page.screenshot({ clip: { x: 0, y: 0, width: p.w, height: p.h } });
      await page.close();
      const dest = p.tmp ? join(scratch, p.file) : join(root, p.file);
      await mkdir(dirname(dest), { recursive: true });
      if (dest.endsWith(".bmp")) {
        const img = decodePng(png);
        const want = [Math.round(p.w * p.scale), Math.round(p.h * p.scale)];
        if (img.width !== want[0] || img.height !== want[1]) problems.push(`${p.file}: ${img.width}x${img.height}, not ${want.join("x")}`);
        await writeFile(dest, encodeBmp(img));
      } else {
        await writeFile(dest, png);
      }
      made.push(p.file);
    }
    // the .dmg's background: both scales in one TIFF, which Finder draws sharp on Retina
    const tiff = join(root, MAC, "dmg-background.tiff");
    await mkdir(dirname(tiff), { recursive: true });
    const cat = join(scratch, "cat.tiff");
    execFileSync("tiffutil", ["-cathidpicheck", join(scratch, "dmg@1x.png"), join(scratch, "dmg@2x.png"), "-out", cat], { stdio: "inherit" });
    execFileSync("tiffutil", ["-lzw", cat, "-out", tiff], { stdio: "inherit" });
    made.push(relative(root, tiff));
    await preview(browser, base, made.filter((f) => f.endsWith(".bmp")), scratch);
  } finally {
    await browser.close();
    server.close();
    await rm(scratch, { recursive: true, force: true });
  }
  return { problems, made };
}

// every piece at 1x on one sheet (Chromium draws BMPs), English above Arabic, for review
async function preview(browser, base, files, scratch) {
  const ones = files.filter((f) => !/-(150|200)\.bmp$/.test(f));
  const cell = (f) =>
    `<figure><img src="/${f}"><figcaption>${f.replace(/^apps\//, "")}</figcaption></figure>`;
  const html = `<!doctype html><meta charset="utf-8"><style>
    body { margin: 0; padding: 24px; background: #2a2f38; font: 12px/16px system-ui, sans-serif; color: #d9dee7;
           display: flex; flex-wrap: wrap; gap: 20px; align-items: flex-start; inline-size: 1160px; }
    figure { margin: 0; } img { display: block; outline: 1px solid #000; } figcaption { margin-block-start: 6px; }
    </style>${ones.map(cell).join("")}
    <figure><img src="/__dmg.png" width="660" height="400"><figcaption>src-tauri/macos/dmg-background.tiff (1x)</figcaption></figure>`;
  const sheet = join(root, "docs/design/desktop/installer-art/__preview.html");
  await writeFile(sheet, html);
  const dmg = join(root, "__dmg.png");
  await writeFile(dmg, await readFile(join(scratch, "dmg@1x.png")));
  try {
    const page = await browser.newPage({ viewport: { width: 1208, height: 800 } });
    await page.goto(`${base}/docs/design/desktop/installer-art/__preview.html`, { waitUntil: "networkidle" });
    await page.screenshot({ path: join(here, "installer-art", "preview.png"), fullPage: true });
    await page.close();
  } finally {
    await rm(sheet, { force: true });
    await rm(dmg, { force: true });
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const { problems, made } = await render();
  if (problems.length) {
    console.error(problems.join("\n"));
    process.exit(1);
  }
  console.log(`installer art: ${made.length} files written`);
}
