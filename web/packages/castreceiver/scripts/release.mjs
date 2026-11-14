// The release's two steps around the build (cast-receiver-release.yml, story 58.07):
//
//   node scripts/release.mjs fetch --live-base https://cast.maktaba.tv --out live-builds.json
//       the live site's builds.json, or an empty list on a first deploy (404)
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
    const res = await get(`${base}/r/builds.json`);
    let body = { builds: [] };
    if (res.ok) body = await res.json();
    else if (res.status !== 404) throw new Error(`${base}/r/builds.json answered ${res.status}`);
    fs.writeFileSync(out, `${JSON.stringify(body, null, 2)}\n`);
    console.log(`live builds: ${(body.builds ?? []).map((b) => b.level).join(", ") || "none"}`);
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
