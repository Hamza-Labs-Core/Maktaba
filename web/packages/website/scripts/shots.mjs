#!/usr/bin/env node
// maktaba.tv's product screenshots (story 60.09): every app's captures on the demo library,
// collected from a CI run and made into the site's images.
//
//   npm run shots -- --from-run <run id>   # download the run's product-shots-* artifacts
//   npm run shots -- --from-dir <dir>      # or read <dir>/<artifact>/<app>-<screen>-<lang>.png
//   (--only web,ipad: those apps alone; --out DIR, --manifest FILE: elsewhere than the site's)
//
// For each app in shots.config.json and each of its screens, in English and Arabic:
//  - crops the capture to the frame's safe area (`crop`, pixels from each edge);
//  - writes WebP and AVIF at each of the app's `widths` (the srcset), without metadata (sharp
//    writes none unless asked), as static/shots/<app>-<screen>-<lang>-<width>.<ext>;
//  - lowers the quality until both files at the largest width are under `budgetBytes`;
//  - records each size's width and height in shots-manifest.json, for the pages' width,
//    height and srcset.
// An app whose CI job did not pass (the run's jobs, matched by `job`), or whose images are not
// all there, keeps its previous images and manifest entries, and the script says so. A run is
// never forced green for screenshots. Android's come from the repository (`repo`): Roborazzi
// references that CI's verify step compares.
//
// Exit status: 0 when every app was taken from this run; 2 when one or more kept their previous
// images (the summary names them); 1 on an error.

import { execFileSync } from "node:child_process";
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
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import sharp from "sharp";

const HERE = dirname(fileURLToPath(import.meta.url));
export const PACKAGE = resolve(HERE, "..");
export const REPO = resolve(PACKAGE, "../../..");

export function loadConfig(file = join(PACKAGE, "shots.config.json")) {
  return JSON.parse(readFileSync(file, "utf8"));
}

/** Every file under `dir`, recursively. */
function walk(dir) {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? walk(join(dir, e.name)) : [join(dir, e.name)]
  );
}

/** Where `app`'s captures are: `name` (app-screen-lang.png) → file, from the run or the repo. */
function findCaptures(app, src, { input, repo }) {
  const files = src.repo
    ? walk(join(repo, src.repo)).filter((f) =>
        f.includes(`${join("src", "test", "screenshots", "demo")}/`)
      )
    : [src.artifact].flat().flatMap((a) => walk(join(input, a)));
  const found = new Map();
  for (const f of files) {
    const name = f.split("/").pop();
    if (name.startsWith(`${app}-`) && name.endsWith(".png"))
      found.set(name.replace(/\.png$/, ""), f);
  }
  return found;
}

/** Why `src`'s jobs in `run` do not count (null when all passed, or no run is known). */
// `artifact` and `job` may each be a list: an app whose screens come from more than one job
function redJob(src, run) {
  if (!run?.jobs) return null;
  for (const name of [src.job].flat()) {
    const jobs = run.jobs.filter((j) => j.name.includes(name));
    if (!jobs.length) return `no job named like "${name}" ran`;
    const bad = jobs.find((j) => j.conclusion !== "success");
    if (bad) return `its job "${bad.name}" concluded ${bad.conclusion || bad.status}`;
  }
  return null;
}

/** Encodes `image` at `width`, at `quality`, both formats. */
async function encode(image, width, quality) {
  const resized = image.clone().resize({ width, withoutEnlargement: true });
  const [webp, avif] = await Promise.all([
    resized.clone().webp({ quality, effort: 6 }).toBuffer({ resolveWithObject: true }),
    resized
      .clone()
      .avif({ quality: Math.max(30, quality - 20), effort: 4 })
      .toBuffer({ resolveWithObject: true }),
  ]);
  return { webp, avif };
}

/** One capture made into the srcset: the files written and their sizes. */
export async function processImage(file, id, src, { out, budget }) {
  const meta = await sharp(file).metadata();
  const c = src.crop ?? {};
  const box = {
    left: c.left ?? 0,
    top: c.top ?? 0,
    width: meta.width - (c.left ?? 0) - (c.right ?? 0),
    height: meta.height - (c.top ?? 0) - (c.bottom ?? 0),
  };
  if (box.width <= 0 || box.height <= 0)
    throw new Error(`${id}: the crop leaves nothing of ${meta.width}×${meta.height}`);
  const base = sharp(file).extract(box);
  const widths = [...src.widths].sort((a, b) => a - b).map((w) => Math.min(w, box.width));
  const largest = widths[widths.length - 1];
  // the quality the largest size fits the budget at, then every size at that quality
  let quality = 82;
  for (;;) {
    const { webp, avif } = await encode(base, largest, quality);
    if (webp.data.length < budget && avif.data.length < budget) break;
    if (quality <= 30)
      throw new Error(`${id}: over ${budget} bytes at ${largest}px even at quality 30`);
    quality -= 6;
  }
  const sizes = [];
  for (const w of [...new Set(widths)]) {
    const { webp, avif } = await encode(base, w, quality);
    const name = `${id}-${w}`;
    writeFileSync(join(out, `${name}.webp`), webp.data);
    writeFileSync(join(out, `${name}.avif`), avif.data);
    sizes.push({
      width: webp.info.width,
      height: webp.info.height,
      webp: `/shots/${name}.webp`,
      avif: `/shots/${name}.avif`,
      webpBytes: webp.data.length,
      avifBytes: avif.data.length,
    });
  }
  const top = sizes[sizes.length - 1];
  return { width: top.width, height: top.height, quality, sizes };
}

/**
 * Makes the site's images from `input` (artifacts, one folder each) and the repository.
 * Returns { manifest, taken: [app], kept: [{ app, why }] }.
 */
export async function makeShots({
  input,
  repo = REPO,
  out,
  manifestPath,
  config = loadConfig(),
  run = null,
  label = "",
}) {
  mkdirSync(out, { recursive: true });
  const previous = existsSync(manifestPath)
    ? JSON.parse(readFileSync(manifestPath, "utf8"))
    : { images: {} };
  const manifest = { from: label || previous.from || "", images: {} };
  const taken = [];
  const kept = [];
  for (const [app, src] of Object.entries(config.sources)) {
    const want = src.screens.flatMap((s) => config.langs.map((l) => `${app}-${s}-${l}`));
    const found = findCaptures(app, src, { input, repo });
    const missing = want.filter((id) => !found.has(id));
    const why = redJob(src, run) ?? (missing.length ? `missing ${missing.join(", ")}` : null);
    if (why) {
      kept.push({ app, why });
      for (const [id, entry] of Object.entries(previous.images))
        if (entry.app === app) manifest.images[id] = { ...entry, kept: true };
      continue;
    }
    // this app's images are all new: its old files go
    for (const f of readdirSync(out))
      if (f.startsWith(`${app}-`) && /\.(webp|avif)$/.test(f)) rmSync(join(out, f));
    for (const id of want) {
      const [screen, lang] = id.slice(app.length + 1).split(/-(?=[a-z]{2}$)/);
      const made = await processImage(found.get(id), id, src, { out, budget: config.budgetBytes });
      manifest.images[id] = { app, screen, lang, ...made };
    }
    taken.push(app);
  }
  manifest.images = Object.fromEntries(
    Object.entries(manifest.images).sort(([a], [b]) => a.localeCompare(b))
  );
  writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + "\n");
  return { manifest, taken, kept };
}

/** Downloads a run's product-shots-* artifacts and its jobs into a new folder. */
function fromRun(id) {
  const dir = mkdtempSync(join(tmpdir(), `shots-${id}-`));
  const repoArg = ["-R", process.env.GH_REPO || "Hamza-Labs-Core/Maktaba"];
  try {
    execFileSync("gh", ["run", "download", id, ...repoArg, "-p", "product-shots-*", "-D", dir], {
      stdio: "inherit",
    });
  } catch {
    console.warn(`shots: run ${id} has no product-shots-* artifact it could download`);
  }
  const run = JSON.parse(
    execFileSync("gh", ["run", "view", id, ...repoArg, "--json", "jobs,headSha,conclusion"], {
      encoding: "utf8",
    })
  );
  return { dir, run };
}

const arg = (name) => {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : null;
};

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const runId = arg("--from-run");
  const dirArg = arg("--from-dir");
  if (!runId && !dirArg) {
    console.error("usage: shots.mjs --from-run <run id> | --from-dir <dir>");
    process.exit(1);
  }
  const { dir, run } = runId
    ? fromRun(runId)
    : {
        dir: resolve(dirArg),
        run: existsSync(join(dirArg, "run.json"))
          ? JSON.parse(readFileSync(join(dirArg, "run.json"), "utf8"))
          : null,
      };
  const out = resolve(arg("--out") ?? join(PACKAGE, "static", "shots"));
  const manifestPath = resolve(arg("--manifest") ?? join(PACKAGE, "shots-manifest.json"));
  const config = loadConfig();
  const only = arg("--only")?.split(",");
  if (only)
    config.sources = Object.fromEntries(
      Object.entries(config.sources).filter(([app]) => only.includes(app))
    );
  makeShots({
    input: dir,
    out,
    manifestPath,
    config,
    run,
    label: runId ? `run ${runId} (${run.headSha?.slice(0, 9)})` : relative(process.cwd(), dir),
  }).then(
    ({ manifest, taken, kept }) => {
      for (const app of taken) {
        const imgs = Object.values(manifest.images).filter((i) => i.app === app);
        const big = Math.max(
          ...imgs.flatMap((i) => i.sizes.map((s) => Math.max(s.webpBytes, s.avifBytes)))
        );
        console.log(
          `shots: ${app}: ${imgs.length} images, largest file ${(big / 1000).toFixed(1)} KB`
        );
      }
      for (const { app, why } of kept)
        console.warn(`shots: ${app}: kept the previous images (${why})`);
      console.log(`shots: wrote ${out} and ${manifestPath}`);
      process.exit(kept.length ? 2 : 0);
    },
    (e) => {
      console.error(`shots: ${e.message}`);
      process.exit(1);
    }
  );
}
