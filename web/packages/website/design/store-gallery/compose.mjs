// Story 60.14 part 2: the full set of store listing images, composed from the apps' captures on
// the demo library, checked against stores.json and laid out where the upload tools read them.
//
//   node design/store-gallery/compose.mjs --captures DIR --out DIR [--only id,id] [--allow-missing]
//   node design/store-gallery/compose.mjs --from-run RUN_ID --out DIR ...   (gh downloads the
//       run's product-shots-* and store-shots-* artifacts into a scratch folder first)
//
// Captures are PNGs named `<surface>-<screen>-<lang>.png`, the names the shot tests already
// write (tools/apple/export-shots.py, the Roborazzi `demo/` references), anywhere under DIR.
// The Remote's screens are `<surface>-remote-<screen>-<lang>.png`. Each listing in stores.json
// is drawn set by set, in the style the set names:
//  - captioned: gallery.css's shot (make.mjs's captioned()), the beat's capture as its screen;
//  - plain: the capture, resized to the store's size (cover; never stretched);
//  - tile, art, feature: make.mjs's tile(), art() and feature();
//  - icon: tools/icons/source/maktaba-icon.png, resized.
// Every image is rendered by Playwright at the store's exact size. It is written as PNG where
// the store takes PNG and it fits the store's limit, otherwise as JPEG at the highest quality
// that fits. Where the store refuses alpha, the image is flattened onto the ink. It carries no
// metadata. Then each file is checked against its manifest entry: dimensions, format, alpha,
// bytes and the set's count; each captioned shot also against the 20% caption share.
//
// Out:
//   apple/<listing>/screenshots/<locale>/NN_<asset>_<beat>.png        fastlane deliver
//   google/<listing>/metadata/android/<locale>/images/...              fastlane supply
//     phoneScreenshots/, sevenInchScreenshots/, tenInchScreenshots/, tvScreenshots/,
//     featureGraphic.png, icon.png, tvBanner.png
//   <store>/<listing>/<lang or all>/<asset>/NN_<beat>.<ext>            Amazon, Roku, Samsung, LG
//   contact/<listing>.png     every image of a listing, small, in order
//   report.json               every file with its size, format, bytes and checks
//   checklist.md              one line per image, to fill in with what it shows (no woman or girl)
//
// Exit status: 0 when every image was made and passed; 1 when one is missing (unless
// --allow-missing, which leaves it out and lists it) or fails a check.

import { execFileSync } from "node:child_process";
import { createServer } from "node:http";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, extname, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { BEATS, STORES, beatsFor, captioned, plain, tile, art, feature, here } from "./make.mjs";
import { measure, MAX_CAPTION_SHARE } from "./render.mjs";
import { SPRITE } from "../mockups/shell.mjs";

const REPO = resolve(here, "..", "..", "..", "..", "..");
const ICON = join(REPO, "tools", "icons", "source", "maktaba-icon.png");
const INK = "#0B111C";

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".woff2": "font/woff2",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".webp": "image/webp",
  ".svg": "image/svg+xml",
};

// fastlane supply's folder for each Google Play screenshot asset, and its file for the rest
const SUPPLY = {
  phone: "phoneScreenshots",
  "tablet-7": "sevenInchScreenshots",
  "tablet-10": "tenInchScreenshots",
  tv: "tvScreenshots",
};
const SUPPLY_FILE = { feature: "featureGraphic", icon: "icon", "tv-banner": "tvBanner" };

// where each art asset puts the lamp (the library hall's subject): right of centre where the
// store covers the left (Amazon: subject right; Samsung: the right 60%)
const ART_AT = {
  "amazon/background": ["14%", "150%"],
  "samsung/detail-background": ["10%", "150%"],
  "samsung/logo-background": ["50%", "cover"],
  "lg/background": ["50%", "cover"],
};

/** Every file under `dir`, recursively. */
function walk(dir) {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? walk(join(dir, e.name)) : [join(dir, e.name)]
  );
}

/** `<surface>-<screen>-<lang>` → file, for every PNG under `dirs` (a later one wins). */
export function indexCaptures(dirs) {
  const out = new Map();
  for (const d of dirs)
    for (const f of walk(d)) if (f.endsWith(".png")) out.set(basename(f, ".png"), f);
  return out;
}

const other = (lang) => (lang === "en" ? "ar" : "en");
const screenOf = (beat, surface) => (beat.capture?.[surface] ?? "").replace(/ new$/, "");

/** The capture a beat shows on a surface in a language: `<surface>-[remote-]<screen>-<lang>`. */
export function captureKey(app, beat, surface, lang) {
  const screen = screenOf(beat, surface === "android-tablet-7" ? "android-tablet" : surface);
  const l = beat.ui === "other" ? other(lang) : lang;
  return `${surface}-${app === "remote" ? "remote-" : ""}${screen}-${l}`;
}

/** Every image of every listing: what to draw, where it goes and what it must be. */
export function plan({ only } = {}) {
  const jobs = [];
  for (const listing of STORES.listings) {
    if (only && !only.includes(listing.id)) continue;
    const store = STORES.stores[listing.store];
    for (const set of listing.sets) {
      const spec = store.assets[set.asset];
      const [W, H] = spec.size;
      // a single image is still written to each locale's folder where fastlane supply reads
      // the listing per locale (Google Play's icon)
      const langs = set.langs === "single" && listing.store !== "google" ? ["all"] : ["en", "ar"];
      for (const lang of langs) {
        const l = lang === "all" ? "en" : lang;
        const base = { listing, store: listing.store, set, spec, W, H, lang };
        if (set.style === "captioned") {
          const beats = beatsFor(
            listing.app,
            set.surface === "android-tablet-7" ? "android-tablet" : set.surface
          );
          beats.forEach((beat, i) =>
            jobs.push({
              ...base,
              n: i + 1,
              name: beat.id,
              beat,
              capture: captureKey(listing.app, beat, set.surface, l),
            })
          );
        } else if (set.style === "plain") {
          const tv = BEATS.apps.tv;
          const picks =
            set.langs === "single"
              ? tv.singleSet[listing.store].map(([id, sl]) => [
                  tv.beats.find((b) => b.id === id),
                  sl,
                ])
              : tv.beats
                  .filter((b) => !tv.drop?.[listing.store]?.includes(b.id))
                  .map((b) => [b, l]);
          picks.forEach(([beat, sl], i) =>
            jobs.push({
              ...base,
              n: i + 1,
              name: beat.id,
              beat,
              capture: captureKey("tv", beat, set.surface, sl),
            })
          );
        } else if (set.style === "feature") {
          // the app's first beat, on a landscape screen of it: the TV's home for the Player and TV,
          // the Remote's first screen on a tablet
          const app = listing.app;
          const capture =
            app === "remote"
              ? captureKey("remote", BEATS.apps.remote.beats[0], "android-tablet", l)
              : captureKey(
                  "tv",
                  BEATS.apps.tv.beats[0],
                  listing.store === "amazon" ? "firetv" : "androidtv",
                  l
                );
          jobs.push({ ...base, n: 1, name: set.asset, capture });
        } else {
          jobs.push({ ...base, n: 1, name: set.asset });
        }
      }
    }
  }
  return jobs;
}

/** Where a job's file goes, without its extension. */
export function pathOf(job) {
  const { listing, store, set, lang, n, name } = job;
  const nn = String(n).padStart(2, "0");
  const locale = lang === "all" ? "all" : (STORES.stores[store].locales?.[lang] ?? lang);
  if (store === "apple")
    return join("apple", listing.id, "screenshots", locale, `${nn}_${set.asset}_${name}`);
  if (store === "google") {
    const images = join("google", listing.id, "metadata", "android", locale, "images");
    return SUPPLY[set.asset]
      ? join(images, SUPPLY[set.asset], `${nn}_${name}`)
      : join(images, SUPPLY_FILE[set.asset]);
  }
  return join(store, listing.id, lang, set.asset, `${nn}_${name}`);
}

/** The page that draws a job, or null for an icon (resized, not rendered). */
function html(job, src) {
  const { set, W, H, lang, beat, listing, store } = job;
  const l = lang === "all" ? "en" : lang;
  switch (set.style) {
    case "captioned":
      return captioned({ W, H, lang: l, beat, surface: set.surface, src });
    case "plain":
      return plain({ W, H, lang: l, beat, surface: set.surface, src });
    case "tile":
      return tile({ W, H, lang: l, both: set.langs === "single", bare: job.spec.alpha === true });
    case "art": {
      const [x, size] = ART_AT[`${store}/${set.asset}`] ?? ["50%", "cover"];
      return art({ W, H, x, size });
    }
    case "feature":
      return feature({ W, H, lang: l, app: listing.app, src });
    default:
      return null;
  }
}

function page(body, W) {
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=${W}">
<link rel="stylesheet" href="/web/packages/website/design/fonts.css">
<link rel="stylesheet" href="/web/packages/website/design/store-gallery/gallery.css">
<style>html,body{margin:0;background:transparent}.sprite{position:absolute}</style></head>
<body>${SPRITE}${body}</body></html>`;
}

/** The repository at /, the captures at /captures/, the job's page at /page.html. */
function serve(captureDir, pages) {
  const server = createServer(async (req, res) => {
    const url = decodeURIComponent(new URL(req.url, "http://x").pathname);
    if (pages.has(url)) {
      res.writeHead(200, { "content-type": TYPES[".html"] });
      return res.end(pages.get(url));
    }
    const [root, rel] = url.startsWith("/captures/") ? [captureDir, url.slice(10)] : [REPO, url];
    const path = resolve(root, "." + (rel.startsWith("/") ? rel : "/" + rel));
    if (path !== root && !path.startsWith(root + sep)) return res.writeHead(403).end();
    try {
      res.writeHead(200, { "content-type": TYPES[extname(path)] ?? "application/octet-stream" });
      res.end(readFileSync(path));
    } catch {
      res.writeHead(404).end();
    }
  });
  return new Promise((ok) => server.listen(0, "127.0.0.1", () => ok(server)));
}

/** PNG where the store takes it and it fits, else the best JPEG that fits; alpha only where allowed. */
async function encode(sharp, buf, spec) {
  let img = sharp(buf);
  if (spec.alpha !== true) img = img.flatten({ background: INK }).removeAlpha();
  const max = spec.maxBytes ?? Infinity;
  if (spec.formats.includes("png")) {
    const png = await img.clone().png({ compressionLevel: 9, adaptiveFiltering: true }).toBuffer();
    if (png.length <= max) return { data: png, ext: "png" };
  }
  if (spec.formats.includes("jpeg")) {
    for (let q = 92; q >= 50; q -= 4) {
      const jpg = await img
        .clone()
        .jpeg({ quality: q, mozjpeg: true, chromaSubsampling: "4:4:4" })
        .toBuffer();
      if (jpg.length <= max) return { data: jpg, ext: "jpg" };
    }
  }
  return null;
}

/** A file against its manifest entry. */
export async function check(sharp, file, spec) {
  const problems = [];
  const meta = await sharp(file).metadata();
  const bytes = readFileSync(file).length;
  const fmt = meta.format === "jpeg" ? "jpeg" : meta.format;
  if (meta.width !== spec.size[0] || meta.height !== spec.size[1])
    problems.push(`${meta.width}x${meta.height}, want ${spec.size.join("x")}`);
  if (!spec.formats.includes(fmt)) problems.push(`${fmt}, want ${spec.formats.join(" or ")}`);
  if (spec.alpha === false && meta.hasAlpha) problems.push("has an alpha channel");
  if (spec.maxBytes && bytes > spec.maxBytes)
    problems.push(`${bytes} bytes, over ${spec.maxBytes}`);
  if (meta.exif || meta.xmp || meta.icc) problems.push("carries metadata");
  return {
    bytes,
    width: meta.width,
    height: meta.height,
    format: fmt,
    hasAlpha: !!meta.hasAlpha,
    problems,
  };
}

export async function compose({ captureDirs, out, only, allowMissing = false, log = console.log }) {
  const { chromium } = await import("@playwright/test");
  const sharp = (await import("sharp")).default;
  const captures = indexCaptures(captureDirs);
  const jobs = plan({ only });
  rmSync(out, { recursive: true, force: true });
  mkdirSync(out, { recursive: true });
  const linkDir = mkdtempSync(join(tmpdir(), "store-gallery-"));
  const pages = new Map();
  const server = await serve(linkDir, pages);
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch();
  const report = [];
  const missing = [];
  const short = new Set(); // sets with a missing capture: their count is not checked when allowed
  const problems = [];
  try {
    for (const job of jobs) {
      const rel = pathOf(job);
      let src;
      if (job.capture) {
        const file = captures.get(job.capture);
        if (!file) {
          missing.push(`${rel}: no capture ${job.capture}.png`);
          short.add(`${job.listing.id}|${job.set.asset}|${job.lang}`);
          continue;
        }
        const link = `${job.capture}.png`;
        writeFileSync(join(linkDir, link), readFileSync(file));
        src = `/captures/${link}`;
      }
      let buf;
      const body = html(job, src);
      if (body === null) {
        buf = await sharp(ICON).resize(job.W, job.H, { fit: "cover" }).png().toBuffer();
      } else {
        pages.set("/page.html", page(body, job.W));
        const ctx = await browser.newContext({
          viewport: { width: job.W, height: job.H },
          deviceScaleFactor: 1,
        });
        const p = await ctx.newPage();
        await p.goto(`${base}/page.html`, { waitUntil: "networkidle" });
        await p.evaluate(() => document.fonts.ready);
        await p.waitForFunction(
          () => [...document.images].every((i) => i.complete && i.naturalWidth > 0),
          null,
          {
            timeout: 15000,
          }
        );
        for (const m of await p.evaluate(measure)) {
          if (m.share > MAX_CAPTION_SHARE)
            problems.push(`${rel}: caption ${(m.share * 100).toFixed(1)}%, over 20%`);
          if (m.hLines > 2 || m.subLines > 2) problems.push(`${rel}: caption over 2 lines`);
        }
        buf = await p.screenshot({
          omitBackground: job.spec.alpha === true,
          clip: { x: 0, y: 0, width: job.W, height: job.H },
        });
        await ctx.close();
      }
      const enc = await encode(sharp, buf, job.spec);
      if (!enc) {
        problems.push(`${rel}: no encoding fits ${job.spec.maxBytes} bytes`);
        continue;
      }
      const file = join(out, `${rel}.${enc.ext}`);
      mkdirSync(dirname(file), { recursive: true });
      writeFileSync(file, enc.data);
      const c = await check(sharp, file, job.spec);
      for (const pr of c.problems) problems.push(`${rel}.${enc.ext}: ${pr}`);
      report.push({
        file: `${rel}.${enc.ext}`,
        listing: job.listing.id,
        asset: job.set.asset,
        lang: job.lang,
        beat: job.name,
        capture: job.capture ?? null,
        ...c,
      });
    }
  } finally {
    await browser.close();
    server.close();
    rmSync(linkDir, { recursive: true, force: true });
  }

  // each set's count, as made, against the store's
  const sets = new Map();
  for (const r of report) {
    const k = `${r.listing}|${r.asset}|${r.lang}`;
    sets.set(k, (sets.get(k) ?? 0) + 1);
  }
  for (const [k, n] of sets) {
    if (allowMissing && short.has(k)) continue;
    const [id, asset] = k.split("|");
    const listing = STORES.listings.find((x) => x.id === id);
    const [min, max] = STORES.stores[listing.store].assets[asset].count;
    if (n < min || n > max) problems.push(`${k}: ${n} images, the store takes ${min}-${max}`);
  }

  // a contact sheet per listing, and the checklist the pull request fills in
  mkdirSync(join(out, "contact"), { recursive: true });
  for (const id of new Set(report.map((r) => r.listing))) {
    const rows = report.filter((r) => r.listing === id);
    const h = 320;
    const thumbs = await Promise.all(
      rows.map((r) =>
        sharp(join(out, r.file)).resize({ height: h }).png().toBuffer({ resolveWithObject: true })
      )
    );
    const perRow = 8;
    const widths = thumbs.map((t) => t.info.width);
    const rowW = [];
    for (let i = 0; i < thumbs.length; i += perRow)
      rowW.push(widths.slice(i, i + perRow).reduce((a, b) => a + b + 12, 12));
    const W = Math.max(...rowW);
    const H = Math.ceil(thumbs.length / perRow) * (h + 12) + 12;
    const comp = [];
    thumbs.forEach((t, i) => {
      const row = Math.floor(i / perRow);
      const left = 12 + widths.slice(row * perRow, i).reduce((a, b) => a + b + 12, 0);
      comp.push({ input: t.data, left, top: 12 + row * (h + 12) });
    });
    await sharp({ create: { width: W, height: H, channels: 3, background: "#05080d" } })
      .composite(comp)
      .png()
      .toFile(join(out, "contact", `${id}.png`));
  }
  writeFileSync(
    join(out, "report.json"),
    JSON.stringify({ images: report, missing, problems }, null, 2) + "\n"
  );
  writeFileSync(
    join(out, "checklist.md"),
    [
      "# Store gallery: what each image shows",
      "",
      "One line per image. Each one is looked at: no woman or girl in any poster, still, frame, avatar, cartoon or name.",
      "",
      "| Image | Capture | Shows | Women or girls |",
      "| --- | --- | --- | --- |",
      ...report.map((r) => `| \`${r.file}\` | ${r.capture ? `\`${r.capture}\`` : "brand"} |  |  |`),
      "",
    ].join("\n")
  );

  log(`${report.length} images written, ${missing.length} missing, ${problems.length} problems`);
  for (const m of missing) log(`missing: ${m}`);
  for (const p of problems) log(`problem: ${p}`);
  return {
    report,
    missing,
    problems,
    ok: problems.length === 0 && (allowMissing || missing.length === 0),
  };
}

/** A run's capture artifacts, downloaded with gh into a scratch folder. */
function fromRun(runId) {
  const dir = mkdtempSync(join(tmpdir(), "store-gallery-run-"));
  execFileSync(
    "gh",
    ["run", "download", String(runId), "-p", "product-shots-*", "-p", "store-shots-*", "-D", dir],
    {
      stdio: "inherit",
    }
  );
  return dir;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const opt = (name) => {
    const i = args.indexOf(name);
    return i >= 0 ? args[i + 1] : undefined;
  };
  const out = opt("--out");
  if (!out) {
    console.error(
      "usage: compose.mjs (--captures DIR | --from-run RUN_ID) --out DIR [--only id,id] [--allow-missing]"
    );
    process.exit(2);
  }
  // the Android and TV references on the demo library live in the repository (Roborazzi `demo/`)
  const repoCaptures = walk(join(REPO, "apps"))
    .filter((f) => f.includes(`${sep}screenshots${sep}demo${sep}`))
    .map(dirname);
  const dirs = [...new Set(repoCaptures)];
  if (opt("--from-run")) dirs.push(fromRun(opt("--from-run")));
  if (opt("--captures")) dirs.push(resolve(opt("--captures")));
  const r = await compose({
    captureDirs: dirs,
    out: resolve(out),
    only: opt("--only")?.split(","),
    allowMissing: args.includes("--allow-missing"),
  });
  process.exit(r.ok ? 0 : 1);
}
