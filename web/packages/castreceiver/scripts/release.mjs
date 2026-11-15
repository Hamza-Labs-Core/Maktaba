// The release's two steps around the build (cast-receiver-release.yml, story 58.07):
//
//   node scripts/release.mjs fetch --live-base https://cast.maktaba.tv --out live-builds.json
//       the live site's builds.json, or an empty list when nothing is deployed yet: the name
//       does not resolve (ENOTFOUND) or the manifest answers 404. Anything else (a 5xx, a refused
//       or reset connection, a TLS error, a manifest that is not {builds: [...]}) fails the
//       release, so a broken live site is never taken for an empty one and overwritten.
//   node scripts/release.mjs copy --live-base https://cast.maktaba.tv [--live live-builds.json]
//       after `npm run build -- --live live-builds.json`: copies each kept build of another level
//       from the live site into dist/r/<level>/, and writes `changed=true|false` to
//       GITHUB_OUTPUT (false: this level's receiver is byte for byte the one live, and the
//       manifest lists the same builds, so there is nothing to deploy)
//
// No secret: the live site is public.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const PKG = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

function arg(name, fallback = "") {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : fallback;
}

async function get(url) {
  const res = await fetch(url, { cache: "no-store", redirect: "follow" });
  return res;
}

/** DNS answers for a name with no record: nothing is deployed there. A temporary resolver
 * failure (EAI_AGAIN) is not one of them: it says nothing about the site. */
const NOT_DEPLOYED_CODES = new Set(["ENOTFOUND"]);

function errorCode(e) {
  // undici's "fetch failed" TypeError carries the socket or DNS error as its cause
  for (let c = e; c; c = c.cause) if (typeof c.code === "string") return c.code;
  return "";
}

/** The live site's manifest, or none when nothing is deployed yet. Returns
 * `{ builds, reason }`; throws on any answer that could be a broken live site. */
export async function fetchLive(base, fetchImpl = fetch) {
  const url = `${base}/r/builds.json`;
  let res;
  try {
    res = await fetchImpl(url, { cache: "no-store", redirect: "follow" });
  } catch (e) {
    const code = errorCode(e);
    if (NOT_DEPLOYED_CODES.has(code)) {
      return {
        builds: [],
        reason: `${new URL(base).hostname} does not resolve (${code}): nothing deployed yet`,
      };
    }
    throw new Error(`${url}: ${e?.message ?? e}${code ? ` (${code})` : ""}`);
  }
  if (res.status === 404)
    return { builds: [], reason: `${url} answered 404: nothing deployed yet` };
  if (!res.ok) throw new Error(`${url} answered ${res.status}`);
  let body;
  try {
    body = await res.json();
  } catch (e) {
    throw new Error(`${url} is not JSON: ${e?.message ?? e}`);
  }
  if (
    !body ||
    !Array.isArray(body.builds) ||
    body.builds.some((b) => !b || typeof b.level !== "number" || typeof b.path !== "string")
  ) {
    throw new Error(`${url} is not a builds manifest ({builds: [{level, path, ...}]})`);
  }
  return { builds: body.builds, reason: "" };
}

/** The deploy is needed unless the fresh manifest lists exactly the live builds, by level and
 * hash. */
export function changed(liveBuilds, freshBuilds) {
  const key = (list) =>
    list
      .map((b) => `${b.level}:${b.hash ?? ""}`)
      .sort()
      .join(",");
  return key(liveBuilds) !== key(freshBuilds);
}

async function main() {
  const cmd = process.argv[2];
  const base = arg("--live-base", "https://cast.maktaba.tv").replace(/\/+$/, "");
  if (cmd === "fetch") {
    const out = arg("--out", "live-builds.json");
    const { builds, reason } = await fetchLive(base);
    if (reason) console.log(reason);
    fs.writeFileSync(out, `${JSON.stringify({ builds }, null, 2)}\n`);
    console.log(`live builds: ${builds.map((b) => b.level).join(", ") || "none"}`);
    return;
  }
  if (cmd === "copy") {
    const fresh = JSON.parse(fs.readFileSync(path.join(PKG, "dist/r/builds.json"), "utf8")).builds;
    const livePath = arg("--live");
    const liveBuilds = livePath ? (JSON.parse(fs.readFileSync(livePath, "utf8")).builds ?? []) : [];
    for (const b of fresh) {
      const dir = path.join(PKG, "dist", b.path);
      if (fs.existsSync(path.join(dir, "receiver.js"))) continue; // this release's own build
      fs.mkdirSync(dir, { recursive: true });
      for (const f of b.files ?? ["receiver.js", "receiver.css"]) {
        const res = await get(`${base}${b.path}${f}`);
        if (!res.ok) throw new Error(`kept build ${b.path}${f} answered ${res.status}`);
        fs.writeFileSync(path.join(dir, f), Buffer.from(await res.arrayBuffer()));
      }
      console.log(`kept build ${b.path} copied from ${base}`);
    }
    const c = changed(liveBuilds, fresh);
    console.log(
      c ? "the receiver changed: deploy" : "the receiver is the one live: nothing to deploy"
    );
    if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `changed=${c}\n`);
    return;
  }
  throw new Error("usage: release.mjs fetch|copy --live-base URL");
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main().catch((e) => {
    console.error(String(e?.message ?? e));
    process.exit(1);
  });
}
