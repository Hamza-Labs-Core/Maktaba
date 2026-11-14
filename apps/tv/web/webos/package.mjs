#!/usr/bin/env node
// Story 58.11 — the LG webOS package: the launcher's build (web/dist-launcher, `pnpm
// build:launcher`) with appinfo.json, its icons and its Arabic title, packed by LG's webOS
// CLI (`ares-package`, @webos-tools/cli, pinned below) into
// com.hamzalabs.maktaba.tv_<version>_all.ipk, then renamed maktaba-webos-<version>.ipk.
//
//   node apps/tv/web/webos/package.mjs --version 1.4.2 [--dist web/dist-launcher] [--out dist]
//   node apps/tv/web/webos/package.mjs --version 1.4.2 --stage-only   (the folder, no .ipk)
//
// No signing: LG signs a package when it is submitted in the Seller Lounge, so no key is
// held anywhere (story 58.11). Source maps stay out of the package.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const ARES_CLI = "@webos-tools/cli@3.2.6";
export const APP_ID = "com.hamzalabs.maktaba.tv";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "../../../..");

/** LG takes a version of three numbers, no leading zeros: v1.4.2 → 1.4.2. */
export function ipkVersion(raw) {
  const v = String(raw ?? "").replace(/^v/, "");
  if (!/^(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})\.(0|[1-9]\d{0,8})$/.test(v))
    throw new Error(`not a webOS version (x.y.z): ${raw}`);
  return v;
}

function copyDir(src, dst, skip) {
  fs.mkdirSync(dst, { recursive: true });
  for (const e of fs.readdirSync(src, { withFileTypes: true })) {
    const s = path.join(src, e.name);
    const d = path.join(dst, e.name);
    if (skip(e.name)) continue;
    if (e.isDirectory()) copyDir(s, d, skip);
    else fs.copyFileSync(s, d);
  }
}

/** The package's folder: the launcher, appinfo.json at the version, icons, resources. */
export function stage({ dist, version, dir }) {
  if (!fs.existsSync(path.join(dist, "index.html")))
    throw new Error(`no launcher build at ${dist} (run pnpm build:launcher)`);
  fs.rmSync(dir, { recursive: true, force: true });
  copyDir(dist, dir, (name) => name.endsWith(".map"));
  const info = JSON.parse(fs.readFileSync(path.join(here, "appinfo.json"), "utf8"));
  info.version = version;
  fs.writeFileSync(path.join(dir, "appinfo.json"), JSON.stringify(info, null, 2) + "\n");
  for (const f of [info.icon, info.largeIcon, info.splashBackground])
    fs.copyFileSync(path.join(here, f), path.join(dir, f));
  copyDir(path.join(here, "resources"), path.join(dir, "resources"), () => false);
  return info;
}

function arg(argv, name, fallback) {
  const i = argv.indexOf(name);
  return i >= 0 && argv[i + 1] ? argv[i + 1] : fallback;
}

function main(argv) {
  const version = ipkVersion(arg(argv, "--version", process.env.VERSION));
  const dist = path.resolve(arg(argv, "--dist", path.join(repo, "web/dist-launcher")));
  const out = path.resolve(arg(argv, "--out", path.join(repo, "dist")));
  const dir = path.join(out, "webos-app");
  stage({ dist, version, dir });
  console.log(`staged ${dir} at ${version}`);
  if (argv.includes("--stage-only")) return;
  execFileSync(
    "npx",
    ["--yes", "--package", ARES_CLI, "ares-package", dir, "--outdir", out, "--no-minify"],
    { stdio: "inherit" }
  );
  const built = path.join(out, `${APP_ID}_${version}_all.ipk`);
  const named = path.join(out, `maktaba-webos-${version}.ipk`);
  fs.renameSync(built, named);
  console.log(`wrote ${named}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
