// Builds cast.maktaba.tv into dist/ (story 58.07):
//
//   dist/r/index.html, loader.js, receiver.css, mark.svg, fonts/   the loader (the registered URL)
//   dist/r/<level>/receiver.js, receiver.css                        this level's receiver build
//   dist/r/builds.json                                              the builds a sender may get
//
//   node scripts/build.mjs [--debug] [--level N] [--live FILE] [--built ISO]
//
// --level is the home's API level the build is made for (default: the newest snapshot in
// shared/api/levels/, the level api/internal/compat names). --live is the live site's
// builds.json (scripts/release.mjs fetches it): its builds stay listed while the 12-month window
// keeps them, and release.mjs copies their files. --debug turns CastDebugLogger and the
// console log on; a release build logs nothing to the device.

import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const PKG = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const REPO = path.resolve(PKG, "../../..");

function arg(name, fallback = "") {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : fallback;
}

/** src/loader/builds.ts, the loader's own window rules, bundled for Node (no second copy). */
export async function loadBuilds() {
  const res = await build({
    entryPoints: [path.join(PKG, "src/loader/builds.ts")],
    bundle: true,
    format: "esm",
    platform: "node",
    write: false,
    logLevel: "warning",
  });
  const code = res.outputFiles[0].text;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString("base64")}`);
}

export function newestLevel(dir = path.join(REPO, "shared/api/levels")) {
  const levels = fs
    .readdirSync(dir)
    .map((f) => /^(\d+)\.yaml$/.exec(f))
    .filter(Boolean)
    .map((m) => Number(m[1]));
  if (!levels.length) throw new Error(`no API level snapshot in ${dir}`);
  return Math.max(...levels);
}

const debug = process.argv.includes("--debug");
const level = Number(arg("--level") || newestLevel());
const built = arg("--built", new Date().toISOString());
const live = arg("--live");
const out = path.join(PKG, "dist");
const r = path.join(out, "r");
const lv = path.join(r, String(level));

fs.rmSync(out, { recursive: true, force: true });
fs.mkdirSync(lv, { recursive: true });

const common = {
  bundle: true,
  format: "esm",
  // the Cast devices' Chromium: module scripts, dynamic import() and import.meta (es2020); a
  // Chromecast 3rd gen and every Google TV device run newer
  target: ["es2020", "chrome80"],
  minify: !debug,
  sourcemap: debug ? "inline" : false,
  define: { __DEBUG__: JSON.stringify(debug) },
  legalComments: "none",
  logLevel: "warning",
};

await build({
  ...common,
  entryPoints: [path.join(PKG, "src/loader/main.ts")],
  outfile: path.join(r, "loader.js"),
});
await build({
  ...common,
  entryPoints: [path.join(PKG, "src/receiver/main.ts")],
  outfile: path.join(lv, "receiver.js"),
});

const css = fs.readFileSync(path.join(PKG, "static/receiver.css"), "utf8");
fs.writeFileSync(path.join(r, "receiver.css"), css);
fs.writeFileSync(path.join(lv, "receiver.css"), css);
fs.copyFileSync(path.join(PKG, "static/mark.svg"), path.join(r, "mark.svg"));
fs.cpSync(path.join(PKG, "static/fonts"), path.join(r, "fonts"), { recursive: true });

let html = fs.readFileSync(path.join(PKG, "static/index.html"), "utf8");
html = html.replace(
  "<!--DEBUG_LOGGER-->",
  debug
    ? '<script src="https://www.gstatic.com/cast/sdk/libs/devtools/debug_layer/caf_receiver_logger.js"></script>'
    : ""
);
fs.writeFileSync(path.join(r, "index.html"), html);

const files = ["receiver.js", "receiver.css"];
const hash = createHash("sha256");
for (const f of files) hash.update(f).update(fs.readFileSync(path.join(lv, f)));
const fresh = { level, path: `/r/${level}/`, built, hash: hash.digest("hex").slice(0, 16), files };

const { withBuild } = await loadBuilds();
const liveBuilds = live ? (JSON.parse(fs.readFileSync(live, "utf8")).builds ?? []) : [];
const builds = withBuild(liveBuilds, fresh, new Date(built));
fs.writeFileSync(path.join(r, "builds.json"), `${JSON.stringify({ builds }, null, 2)}\n`);

console.log(
  `cast receiver: level ${level} (${fresh.hash})${debug ? ", debug" : ""}; builds ${builds.map((b) => b.level).join(", ")}`
);
