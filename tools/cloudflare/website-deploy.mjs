// maktaba.tv's deploy (Epic 60 README §6.3, story 60.03), run by website-deploy.yml after the
// token check, from web/packages/website with its dist/ in place:
//
//   node website-deploy.mjs deploy --sha <commit> [--config <file>] [--live-base <url>]
//   node website-deploy.mjs rollback --actor <who> [--version <id>] [--config <file>]
//
// deploy: record the version serving now (`deployments status`), upload the new one
// (`versions upload`, not live, with a preview URL), smoke its preview, promote it at 100%
// (`versions deploy`) and apply the custom domains (`triggers deploy`), then smoke the live
// site. When anything after the upload's preview fails, it rolls back to the recorded version
// (on a first deploy, with nothing to return to, it detaches the custom domains instead) and
// fails.
//
// A first deploy: `versions upload` cannot create a Worker, so the Worker is first created by
// `wrangler deploy` with the same config less its routes (bootstrapConfig): it serves no name
// (workers_dev = false) and turns on the preview URLs. The custom domains are attached only by
// the `triggers deploy` after the staged version passed its preview smoke.
//
// --config <file> runs every wrangler call with that config, and --live-base <url> smokes the
// live site as `website-preview` at <url> (a workers.dev name answers no www, http or app.
// check): together, a proof of the whole flow on a throwaway Worker on workers.dev.
//
// rollback: the given version, or else the version of the deployment before the current one
// (`deployments list`), then the live smoke.
//
// Env: WRANGLER (the wrangler binary; default the package's pinned one), CLOUDFLARE_API_TOKEN
// and CLOUDFLARE_ACCOUNT_ID (wrangler's, and the domains' detach on a failed first deploy),
// GITHUB_OUTPUT (previous_id and version_id are written to it when set). Nothing here prints
// the token or the account id.

import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { client, DEFAULT_BASE } from "./cf.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
export const PKG = path.resolve(HERE, "../../web/packages/website");
export const WORKER = "maktaba-website";
export const DOMAINS = ["maktaba.tv", "www.maktaba.tv"];

/** Runs wrangler from the package: answers { code, out } (stdout and stderr together). A call
 * may name its own config (the first deploy's bootstrap). */
export function wranglerRunner(bin = process.env.WRANGLER || path.join(PKG, "node_modules/.bin/wrangler"), cwd = PKG, config = "") {
  return (args, extraEnv = {}, cfg = config) => {
    const argv = cfg ? [...args, "--config", cfg] : args;
    const r = spawnSync(bin, argv, { cwd, encoding: "utf8", env: { ...process.env, ...extraEnv }, maxBuffer: 64 << 20 });
    if (r.error) throw r.error;
    const out = `${r.stdout ?? ""}${r.stderr ?? ""}`;
    return { code: r.status ?? 1, out };
  };
}

function must(w, args, what, cfg) {
  const r = cfg === undefined ? w(args) : w(args, {}, cfg);
  if (r.code !== 0) throw new Error(`${what} failed (wrangler ${args[0]} ${args[1] ?? ""}):\n${r.out.slice(-2000)}`);
  return r.out;
}

/** The JSON a `--json` command printed (wrangler may print a banner line before it). */
export function parseJson(out) {
  const i = out.search(/^[[{]/m);
  if (i < 0) throw new Error(`no JSON in wrangler's output:\n${out.slice(0, 500)}`);
  return JSON.parse(out.slice(i));
}

/** The version a deployment serves: its only one, or the one with the largest share. */
export function servingVersion(deployment) {
  const vs = [...(deployment?.versions ?? [])].sort((a, b) => b.percentage - a.percentage);
  if (!vs.length) throw new Error("the deployment lists no version");
  return vs[0].version_id;
}

/** The version serving now, or null when the Worker does not exist yet (a first deploy). Any
 * other failure stops the deploy: a deploy that could not record what to return to does not
 * start. */
export function recordLive(w) {
  const r = w(["deployments", "status", "--json"]);
  if (r.code !== 0) {
    if (/\[code: 10007\]|This Worker does not exist/.test(r.out)) return null;
    throw new Error(`could not read the live deployment (wrangler deployments status):\n${r.out.slice(-2000)}`);
  }
  return servingVersion(parseJson(r.out));
}

/** The version of the deployment before the current one (`deployments list`, by date). */
export function previousDeployed(w) {
  const list = parseJson(must(w, ["deployments", "list", "--json"], "reading the deployments"));
  const sorted = [...list].sort((a, b) => Date.parse(a.created_on) - Date.parse(b.created_on));
  if (sorted.length < 2) throw new Error("no deployment before the current one to roll back to");
  return servingVersion(sorted[sorted.length - 2]);
}

/** Uploads a version (not live): its id and preview URL, from wrangler's output file (the
 * `version-upload` entry), else from its printed lines. */
export function upload(w, sha) {
  const file = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "website-deploy-")), "out.ndjson");
  const r = w(["versions", "upload", "--message", sha], { WRANGLER_OUTPUT_FILE_PATH: file });
  if (r.code !== 0) throw new Error(`the upload failed (wrangler versions upload):\n${r.out.slice(-2000)}`);
  let id = "";
  let url = "";
  if (fs.existsSync(file)) {
    for (const line of fs.readFileSync(file, "utf8").split("\n").filter(Boolean)) {
      const e = JSON.parse(line);
      if (e.type === "version-upload") {
        id = e.version_id ?? id;
        url = e.preview_url ?? url;
      }
    }
  }
  id ||= r.out.match(/Worker Version ID:\s*([0-9a-f-]{36})/)?.[1] ?? "";
  url ||= r.out.match(/Version Preview URL:\s*(https:\/\/\S+)/)?.[1] ?? "";
  if (!id) throw new Error(`the upload printed no version id:\n${r.out.slice(-2000)}`);
  if (!url) throw new Error("the upload has no preview URL: is preview_urls = true in wrangler.toml?");
  return { id, url };
}

/** The config of a first deploy's bootstrap: the same Worker, its assets and its preview URLs,
 * with no routes and no workers.dev name, so it serves nothing until `triggers deploy`. */
export function bootstrapConfig(toml) {
  const out = toml
    .replace(/^routes\s*=\s*\[[\s\S]*?^\]\s*$/m, "")
    .replace(/^workers_dev\s*=.*$/m, "workers_dev = false");
  if (/^\s*routes?\s*=|custom_domain|^\s*\[\[routes\]\]/m.test(out))
    throw new Error("the bootstrap config still has a route");
  if (!/^workers_dev = false$/m.test(out)) throw new Error("the config has no workers_dev line");
  if (!/^preview_urls\s*=\s*true\s*(#.*)?$/m.test(out)) throw new Error("the config does not turn on preview_urls");
  return out;
}

/** Creates the Worker for a first deploy (bootstrapConfig), from a config next to the real one
 * (its relative paths still hold), removed afterwards. */
export function bootstrap(w, configPath, sha) {
  const tmp = path.join(path.dirname(configPath), ".wrangler-bootstrap.toml");
  fs.writeFileSync(tmp, bootstrapConfig(fs.readFileSync(configPath, "utf8")));
  try {
    must(w, ["deploy", "--message", `website-deploy bootstrap at ${sha}`], "creating the Worker", tmp);
  } finally {
    fs.rmSync(tmp, { force: true });
  }
}

/** Runs a smoke target (tools/cloudflare/smoke.mjs) as its own process; true when it passed. */
export function smokeRunner(log = console.log) {
  return (target, base = "") => {
    const args = [path.join(HERE, "smoke.mjs"), target, ...(base ? ["--base", base] : [])];
    const r = spawnSync(process.execPath, args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
    log(`${r.stdout ?? ""}${r.stderr ?? ""}`.trimEnd());
    return r.status === 0;
  };
}

/** Detaches the Worker's custom domains (a failed first deploy: no version to return to).
 * Detaching a custom domain removes the DNS record Cloudflare made for it. */
export async function detachDomains({ token, accountId, base = DEFAULT_BASE, fetchImpl = fetch, log = console.log }) {
  const cf = client({ token, base, fetchImpl });
  const list = (await cf.get(`/accounts/${accountId}/workers/domains?service=${WORKER}`)) ?? [];
  const ours = list.filter((d) => d.service === WORKER && DOMAINS.includes(d.hostname));
  for (const d of ours) {
    await cf.delete(`/accounts/${accountId}/workers/domains/${d.id}`);
    log(`detached ${d.hostname}`);
  }
  return ours.map((d) => d.hostname);
}

function output(name, value) {
  if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `${name}=${value}\n`);
}

/** The deploy (README §6.3 steps 3 to 6). Throws on any failure, after rolling back. */
export async function deploy({ w, smoke, detach, sha, config = path.join(PKG, "wrangler.toml"), live = { target: "website" }, log = console.log }) {
  // 3. what a rollback returns to; a bare `wrangler rollback` would pick the version uploaded
  // before the latest, which can be one that never passed its preview
  const previous = recordLive(w);
  log(previous ? `live version: ${previous}` : "no live version: this is the first deploy");
  output("previous_id", previous ?? "none");
  if (!previous) {
    bootstrap(w, config, sha);
    log("created the Worker (no routes, no workers.dev name: it serves nothing yet)");
  }

  // 4. stage: a new version, not live, with its own preview URL
  const { id, url } = upload(w, sha);
  log(`uploaded version ${id}, preview ${url}`);
  output("version_id", id);
  if (!smoke("website-preview", url)) throw new Error(`the preview of ${id} failed its smoke; nothing was promoted`);

  // 5. promote in one switch, then apply the custom domains; 6. smoke the live site
  try {
    must(w, ["versions", "deploy", `${id}@100%`, "--yes", "--message", sha], "the promotion");
    must(w, ["triggers", "deploy"], "applying the custom domains");
    if (!smoke(live.target, live.base)) throw new Error(`the live site failed its smoke after promoting ${id}`);
  } catch (e) {
    if (previous) {
      log(`rolling back to ${previous}`);
      must(w, ["rollback", previous, "--message", `website-deploy: live smoke failed at ${sha}`, "--yes"], "the rollback");
    } else {
      log("first deploy: detaching the custom domains");
      await detach();
    }
    throw e;
  }
  log(`the Worker serves ${id}`);
}

/** The rollback by hand (README §6.3 step 7). */
export async function rollback({ w, smoke, version = "", actor, live = { target: "website" }, log = console.log }) {
  const id = version || previousDeployed(w);
  log(`rolling back to ${id}`);
  must(w, ["rollback", id, "--message", `website-deploy rollback by ${actor}`, "--yes"], "the rollback");
  output("version_id", id);
  if (!smoke(live.target, live.base)) throw new Error(`the live site failed its smoke after rolling back to ${id}`);
  log(`the Worker serves ${id}`);
}

async function main() {
  const [cmd, ...args] = process.argv.slice(2);
  const arg = (k, d = "") => {
    const i = args.indexOf(k);
    return i >= 0 ? args[i + 1] : d;
  };
  const config = path.resolve(arg("--config", path.join(PKG, "wrangler.toml")));
  const w = wranglerRunner(undefined, PKG, config);
  const smoke = smokeRunner();
  const liveBase = arg("--live-base");
  const live = liveBase ? { target: "website-preview", base: liveBase } : { target: "website" };
  if (cmd === "deploy") {
    const sha = arg("--sha");
    if (!sha) throw new Error("--sha is required");
    const detach = () =>
      detachDomains({
        token: (process.env.CLOUDFLARE_API_TOKEN ?? "").trim(),
        accountId: (process.env.CLOUDFLARE_ACCOUNT_ID ?? "").trim(),
        base: process.env.CLOUDFLARE_API_BASE || DEFAULT_BASE,
      });
    await deploy({ w, smoke, detach, sha, config, live });
  } else if (cmd === "rollback") {
    await rollback({ w, smoke, version: arg("--version"), actor: arg("--actor", "unknown"), live });
  } else {
    throw new Error("usage: website-deploy.mjs deploy --sha <commit> | rollback --actor <who> [--version <id>]");
  }
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((e) => {
    console.log(`::error title=maktaba.tv deploy::${e.message.split("\n")[0]}`);
    console.log(e.message);
    process.exit(1);
  });
}
