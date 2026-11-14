// Builds the Roku channel (Epic 58 story 58.12, T02): BrighterScript compiles and type-checks
// the channel with bslint's rules, the staged files gain the bundled fonts and the generated
// images, and the zip is written and held under Roku's size limit.
//
//   node tools/build.mjs                       # build/maktaba-roku.zip (a store build)
//   node tools/build.mjs --test --cloud URL --probe-ip IP --probe-port N --out FILE
//                                              # a test build for the brs-engine screen tests
//
// The fonts are the repository's own IBM Plex Sans Arabic (Latin included, SIL OFL; the Apple
// apps carry the same files): Roku's system font has no Arabic (story 58.15). The images
// (focus icons, splash) are drawn from the Maktaba mark on the Ink & Brass background.

import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const ROKU = join(dirname(fileURLToPath(import.meta.url)), "..");
const REPO = join(ROKU, "..", "..", "..");
const STAGE = join(ROKU, "build", "stage");
const FONTS = join(REPO, "apps", "apple", "MaktabaKit", "Sources", "MaktabaKit", "Resources", "Fonts");
const LOGO = join(REPO, "apps", "apple", "MaktabaKit", "Sources", "MaktabaKit", "Resources", "Art", "maktaba-logo.png");
/** Roku's limit for a channel package. */
export const MAX_ZIP_BYTES = 4 * 1024 * 1024;

function args(argv) {
  const out = { test: false, out: join(ROKU, "build", "maktaba-roku.zip") };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--test") out.test = true;
    else if (a === "--cloud") out.cloud = argv[++i];
    else if (a === "--probe-ip") out.probeIp = argv[++i];
    else if (a === "--probe-port") out.probePort = argv[++i];
    else if (a === "--out") out.out = argv[++i];
    else throw new Error(`unknown argument ${a}`);
  }
  return out;
}

/** BrighterScript with bslint: fails on any error diagnostic. */
function compile() {
  rmSync(STAGE, { recursive: true, force: true });
  const cli = require.resolve("brighterscript/dist/cli.js");
  execFileSync(process.execPath, [cli, "--project", join(ROKU, "bsconfig.json")], { cwd: ROKU, stdio: "inherit" });
}

async function images() {
  const { createCanvas, loadImage } = require("canvas");
  const logo = await loadImage(readFileSync(LOGO));
  mkdirSync(join(STAGE, "images"), { recursive: true });
  const draw = (name, w, h, logoH) => {
    const c = createCanvas(w, h);
    const g = c.getContext("2d");
    g.fillStyle = "#0B111C";
    g.fillRect(0, 0, w, h);
    g.drawImage(logo, (w - logoH) / 2, (h - logoH) / 2, logoH, logoH);
    writeFileSync(join(STAGE, "images", name), c.toBuffer("image/png"));
  };
  draw("icon_focus_hd.png", 336, 210, 170);
  draw("icon_focus_fhd.png", 540, 405, 300);
  draw("splash_fhd.png", 1920, 1080, 360);
}

function manifest(o) {
  const path = join(STAGE, "manifest");
  let text = readFileSync(path, "utf8");
  if (o.test) {
    text = text.replace(/^bs_const=TEST=false$/m, "bs_const=TEST=true");
    if (o.cloud) text += `maktaba_cloud=${o.cloud}\n`;
    if (o.probeIp) text += `maktaba_probe_ip=${o.probeIp}\n`;
    if (o.probePort) text += `maktaba_probe_port=${o.probePort}\n`;
  }
  writeFileSync(path, text);
}

export async function build(argv = []) {
  const o = args(argv);
  compile();
  mkdirSync(join(STAGE, "fonts"), { recursive: true });
  for (const f of ["IBMPlexSansArabic-Regular.ttf", "IBMPlexSansArabic-SemiBold.ttf", "OFL-IBMPlexSansArabic.txt"]) {
    copyFileSync(join(FONTS, f), join(STAGE, "fonts", f));
  }
  await images();
  manifest(o);
  const { RokuDeploy } = require("roku-deploy");
  rmSync(o.out, { force: true });
  mkdirSync(dirname(o.out), { recursive: true });
  await new RokuDeploy().zipFolder(STAGE, o.out);
  const size = statSync(o.out).size;
  console.log(`built ${o.out}: ${size} bytes (limit ${MAX_ZIP_BYTES})`);
  if (size > MAX_ZIP_BYTES) throw new Error(`the channel is ${size} bytes, over Roku's ${MAX_ZIP_BYTES}`);
  return o.out;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  build(process.argv.slice(2)).catch((e) => {
    console.error(e.message);
    process.exit(1);
  });
}
