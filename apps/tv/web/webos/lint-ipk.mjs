#!/usr/bin/env node
// Story 58.11 T02 — the LG package lint: opens a built .ipk (an `ar` archive holding
// debian-binary, control.tar.gz and data.tar.gz), reads the app's appinfo.json from
// usr/palm/applications/<id>/ and checks it is what the store and the launcher need:
//
//   id, version (x.y.z, the one asked for), vendor, type web, main (present), title (and the
//   Arabic one), icon 80×80 and largeIcon 130×130 PNGs (present), splashBackground,
//   resolution 1920x1080, disableBackHistoryAPI true (the app handles Back, key 461),
//   requiredACG exactly the luna groups the launcher calls (webOS 26 on enforce it);
//   the launcher's page and script inside; no source map, no file over 5 MB, and nothing
//   that looks like a key or a token.
//
//   node apps/tv/web/webos/lint-ipk.mjs dist/maktaba-webos-1.4.2.ipk [--version 1.4.2]

import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
import { fileURLToPath } from "node:url";

export const APP_ID = "com.hamzalabs.maktaba.tv";
const MAX_FILE = 5 * 1024 * 1024;
/** connectionmanager getStatus, tv.systemproperty getSystemInfo (LG's ACG guide). */
export const REQUIRED_ACG = ["network.query", "systemconfig.query"];

/** The members of an `ar` archive: name → bytes. */
export function readAr(buf) {
  if (buf.subarray(0, 8).toString("latin1") !== "!<arch>\n") throw new Error("not an ar archive");
  const out = new Map();
  let at = 8;
  while (at + 60 <= buf.length) {
    const head = buf.subarray(at, at + 60).toString("latin1");
    const name = head.slice(0, 16).trim().replace(/\/$/, "");
    const size = Number(head.slice(48, 58).trim());
    if (!Number.isFinite(size)) throw new Error("a bad ar header");
    out.set(name, buf.subarray(at + 60, at + 60 + size));
    at += 60 + size + (size % 2);
  }
  return out;
}

/** The files of a tar archive (gzipped or not): path (without ./) → bytes. */
export function readTar(buf) {
  const raw = buf[0] === 0x1f && buf[1] === 0x8b ? zlib.gunzipSync(buf) : buf;
  const out = new Map();
  let at = 0;
  let longName = null;
  while (at + 512 <= raw.length) {
    const h = raw.subarray(at, at + 512);
    if (h.every((b) => b === 0)) break;
    const str = (a, b) => h.subarray(a, b).toString("utf8").replace(/\0.*$/s, "");
    let name = str(0, 100);
    const prefix = str(345, 500);
    if (prefix) name = `${prefix}/${name}`;
    const size = parseInt(str(124, 136).trim() || "0", 8);
    const type = String.fromCharCode(h[156] || 48);
    const body = raw.subarray(at + 512, at + 512 + size);
    at += 512 + Math.ceil(size / 512) * 512;
    if (type === "L") {
      longName = body.toString("utf8").replace(/\0.*$/s, "");
      continue;
    }
    if (longName) {
      name = longName;
      longName = null;
    }
    if (type === "0" || type === "\0") out.set(name.replace(/^\.\//, ""), body);
  }
  return out;
}

/** A PNG's width and height (null when it is not a PNG). */
export function pngSize(buf) {
  if (!buf || buf.length < 24 || buf.readUInt32BE(0) !== 0x89504e47) return null;
  return { w: buf.readUInt32BE(16), h: buf.readUInt32BE(20) };
}

const SECRET = [
  /-----BEGIN [A-Z ]*PRIVATE KEY-----/,
  /\bAKIA[0-9A-Z]{16}\b/,
  /\bgh[pousr]_[A-Za-z0-9]{36}\b/,
  /\bsk_(?:live|test)_[A-Za-z0-9]{16,}/,
];

/** What is wrong with an app's files (path inside the app folder → bytes); [] when nothing. */
export function lintApp(files, want = {}) {
  const errors = [];
  const raw = files.get("appinfo.json");
  if (!raw) return ["appinfo.json is missing"];
  let info;
  try {
    info = JSON.parse(raw.toString("utf8"));
  } catch (e) {
    return [`appinfo.json is not JSON: ${e.message}`];
  }
  const need = (cond, msg) => cond || errors.push(msg);
  need(info.id === APP_ID, `id is ${JSON.stringify(info.id)}, not ${APP_ID}`);
  need(
    /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(info.version ?? ""),
    `version ${JSON.stringify(info.version)} is not x.y.z`
  );
  if (want.version)
    need(info.version === want.version, `version is ${info.version}, not ${want.version}`);
  need(info.version !== "0.0.0", "version is the template's 0.0.0");
  need(typeof info.vendor === "string" && info.vendor.length > 0, "vendor is missing");
  need(info.type === "web", `type is ${JSON.stringify(info.type)}, not web`);
  need(
    info.main === "index.html" && files.has("index.html"),
    "main is not index.html, or it is missing"
  );
  need(info.title === "Maktaba", `title is ${JSON.stringify(info.title)}`);
  need(info.resolution === "1920x1080", `resolution is ${JSON.stringify(info.resolution)}`);
  need(
    info.disableBackHistoryAPI === true,
    "disableBackHistoryAPI is not true (the app handles Back)"
  );
  // the luna groups the launcher calls (webos-luna.ts), and only those
  need(
    JSON.stringify(info.requiredACG) === JSON.stringify(REQUIRED_ACG),
    `requiredACG is ${JSON.stringify(info.requiredACG)}, not ${JSON.stringify(REQUIRED_ACG)}`
  );
  for (const [key, size] of [
    ["icon", 80],
    ["largeIcon", 130],
  ]) {
    const s = pngSize(files.get(info[key]));
    need(s && s.w === size && s.h === size, `${key} (${info[key]}) is not a ${size}×${size} PNG`);
  }
  const splash = pngSize(files.get(info.splashBackground));
  need(splash && splash.w === 1920 && splash.h === 1080, "splashBackground is not a 1920×1080 PNG");
  const ar = files.get("resources/ar/appinfo.json");
  need(
    !!ar && JSON.parse(ar.toString("utf8")).title === "مكتبة",
    "the Arabic title (resources/ar) is missing"
  );
  const html = files.get("index.html")?.toString("utf8") ?? "";
  const scripts = [...html.matchAll(/<script[^>]*\ssrc="([^"]+)"/g)].map((m) => m[1]);
  need(scripts.length > 0, "index.html loads no script");
  for (const s of scripts) {
    need(
      !/^(?:[a-z]+:)?\/\//i.test(s) && !s.startsWith("/"),
      `index.html loads ${s}, not a relative file`
    );
    need(files.has(s.replace(/^\.\//, "")), `index.html loads ${s}, which is not in the package`);
  }
  need(
    !/<script[^>]*type="module"/.test(html),
    "index.html has a module script (a file:// page may refuse it)"
  );
  for (const [name, body] of files) {
    need(!name.endsWith(".map"), `${name}: a source map in the package`);
    need(body.length <= MAX_FILE, `${name}: over 5 MB`);
    if (/\.(js|json|html|css|txt)$/.test(name)) {
      const text = body.toString("utf8");
      for (const re of SECRET)
        need(!re.test(text), `${name}: looks like it holds a secret (${re.source})`);
    }
  }
  return errors;
}

/** An .ipk's app files (inside usr/palm/applications/<id>/). */
export function appFiles(ipk) {
  const members = readAr(ipk);
  const dataName = [...members.keys()].find((n) => n.startsWith("data.tar"));
  if (!dataName) throw new Error("no data.tar in the package");
  if (
    !members.has("control.tar.gz") &&
    ![...members.keys()].some((n) => n.startsWith("control.tar"))
  )
    throw new Error("no control.tar in the package");
  const prefix = `usr/palm/applications/${APP_ID}/`;
  const files = new Map();
  for (const [name, body] of readTar(members.get(dataName))) {
    if (name.startsWith(prefix)) files.set(name.slice(prefix.length), body);
  }
  if (files.size === 0) throw new Error(`nothing under ${prefix}`);
  return files;
}

function main(argv) {
  const file = argv.find((a) => !a.startsWith("--"));
  const vi = argv.indexOf("--version");
  if (!file) {
    console.error("usage: lint-ipk.mjs <package.ipk> [--version x.y.z]");
    process.exit(2);
  }
  const files = appFiles(fs.readFileSync(file));
  const errors = lintApp(files, { version: vi >= 0 ? argv[vi + 1] : undefined });
  if (errors.length) {
    for (const e of errors) console.error(`::error file=${path.basename(file)}::${e}`);
    process.exit(1);
  }
  const info = JSON.parse(files.get("appinfo.json").toString("utf8"));
  console.log(
    `${path.basename(file)}: ${info.id} ${info.version}, ${files.size} files, appinfo.json as listed`
  );
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
