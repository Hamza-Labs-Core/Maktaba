#!/usr/bin/env node
// The broadcast rehearsal's edge (Epic 50 story 50.29): web/packages/broadcast-edge's Worker in
// Miniflare, as several simulated Cloudflare locations, with local R2, Durable Objects and a fake
// Analytics Engine. No call leaves the container and no Cloudflare account is used.
//
//   :8787  the edge. A request goes to the location named by its X-Rehearsal-Location header (0
//          when absent); each location is its own Worker with its own cache, and every location
//          shares the Durable Objects and the R2 buckets, as Cloudflare's do. Two versions of the
//          Worker run side by side for the edge-deploy scenario; /__rehearsal/rollout?pct=N sends
//          N % of sessions (X-Rehearsal-Session) to the new one. /__rehearsal/counters sums every
//          Worker's counters (entry.ts) and lists them per location.
//   :8788  this server's control: GET /healthz; POST /deploy {"vars": {...}} redeploys every
//          Worker with those variables (the break-glass variables of 50.25, a new version),
//          keeping the Durable Objects' state on disk.
//
// Two fakes stand in for what the edge calls out to, so nothing leaves the container:
//   - Turnstile's siteverify (the edge's TURNSTILE_API binding, 50.26): a token "pass-…" is good
//     once (a replay answers timeout-or-duplicate, as Turnstile does); any other fails.
//   - the cloud's API (API_ORIGIN, every Worker's outbound fetch): it takes the edge's signed
//     usage posts (50.20 §3) and answers as the cloud does (taken, duplicates). /__rehearsal/cloud/
//     down and /up stop and start it (the Hetzner pair down, 14.8); /__rehearsal/cloud/usage is
//     every post it saw, with the edge's signature headers, and every window it took.
//
// REHEARSAL_PLANT=per-viewer-fetch turns every location's cache off (entry.ts): each viewer's segment is then
// an R2 read. It is the planted regression the fan-out self-test must fail on (story 50.29 T03).
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { mkdirSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { convertV4MiniflareOptions, Miniflare } from "miniflare";

const here = dirname(fileURLToPath(import.meta.url));
const EDGE = resolve(process.env.EDGE_DIR ?? join(here, "../../../../web/packages/broadcast-edge"));
const LOCATIONS = Number(process.env.REHEARSAL_LOCATIONS ?? 5);
const PORT = Number(process.env.PORT ?? 8787);
const CONTROL_PORT = Number(process.env.CONTROL_PORT ?? 8788);
const DATA = process.env.REHEARSAL_DATA ?? "/tmp/broadcast-edge-rehearsal";
const PLANT = process.env.REHEARSAL_PLANT ?? "";
// Rehearsal-only keys: the cloud's TEST key "cloud-1" (cloud/internal/broadcast/testdata) and a
// viewer key whose name says what it is. Never a real key.
const VIEWER_KEY = "cmVoZWFyc2FsLXZpZXdlci1rZXktbmV2ZXItcHJvZHVjdGlvbg";
// The edge's EDGE_SIGNING_KEY: the SHA-256 of a label that says what it is (tools/broadcast-viewers
// RehearsalEdgeSeed), and a Turnstile secret no Turnstile knows.
const EDGE_SEED = createHash("sha256")
  .update("maktaba-broadcast rehearsal edge key, never for production")
  .digest("base64url");
const TURNSTILE_SECRET = "rehearsal-turnstile-secret-not-real";
const CLOUD_ORIGIN = "http://cloud.rehearsal.invalid";
const vectors = JSON.parse(
  readFileSync(join(EDGE, "../../../cloud/internal/broadcast/testdata/vectors.json"), "utf8")
);

const bundle = await build({
  entryPoints: [join(here, "entry.ts")],
  bundle: true,
  write: false,
  format: "esm",
  platform: "neutral",
  target: "es2022",
  conditions: ["workerd", "worker", "browser"],
  external: ["cloudflare:*", "node:*"],
  alias: { "@edge": join(EDGE, "src") },
  logLevel: "warning",
});
const script = bundle.outputFiles[0].text;

const ROUTER = `
const LOCS = ${LOCATIONS};
let pct = 0;
function hash(s) { let h = 2166136261; for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); } return h >>> 0; }
export default {
  async fetch(req, env) {
    const url = new URL(req.url);
    if (url.pathname === "/__rehearsal/rollout") {
      const p = Number(url.searchParams.get("pct"));
      if (req.method !== "POST" || !(p >= 0 && p <= 100)) return new Response("bad", { status: 400 });
      pct = p;
      return Response.json({ pct });
    }
    if (url.pathname.startsWith("/__rehearsal/cloud/")) return env.CLOUD.fetch(req);
    if (url.pathname.startsWith("/__rehearsal/")) {
      const workers = [];
      for (const [name, svc] of Object.entries(env)) {
        if (!/^L\\d+(_V2)?$/.test(name)) continue;
        const r = await svc.fetch(new Request(req.url, { method: req.method }));
        workers.push({ worker: name, location: Number(name.slice(1).split("_")[0]), version: name.endsWith("_V2") ? 2 : 1, ...(await r.json()) });
      }
      return Response.json({ pct, locations: LOCS, workers });
    }
    const loc = (Number(req.headers.get("X-Rehearsal-Location") ?? "0") || 0) % LOCS;
    const key = req.headers.get("X-Rehearsal-Session") ?? url.pathname;
    const v2 = pct > 0 && hash(key) % 100 < pct;
    return env["L" + loc + (v2 ? "_V2" : "")].fetch(req);
  },
};`;

// siteverify: "pass-…" is good once; a replay or anything else fails (50.26 §2)
const TURNSTILE = `
const seen = new Set();
export default {
  async fetch(req) {
    const form = await req.formData();
    const token = String(form.get("response") ?? "");
    const ok = form.get("secret") === ${JSON.stringify(TURNSTILE_SECRET)} && token.startsWith("pass-") && !seen.has(token);
    const codes = seen.has(token) ? ["timeout-or-duplicate"] : ok ? [] : ["invalid-input-response"];
    seen.add(token);
    return Response.json({ success: ok, "error-codes": codes });
  },
};`;

// the cloud's API as the edge sees it: usage posts (cloud/internal/broadcast.UsageReply)
const CLOUD = `
let down = false;
const posts = [];
const windows = {};
let refused = 0;
export default {
  async fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === "/__rehearsal/cloud/down" && req.method === "POST") { down = true; return Response.json({ down }); }
    if (url.pathname === "/__rehearsal/cloud/up" && req.method === "POST") { down = false; return Response.json({ down }); }
    if (url.pathname === "/__rehearsal/cloud/usage") return Response.json({ down, refused, posts, windows });
    if (down) { refused++; return new Response("the cloud is down", { status: 503 }); }
    if (url.pathname === "/_edge/usage" && req.method === "POST") {
      const body = await req.text();
      posts.push({ method: req.method, path: url.pathname, body,
        time: req.headers.get("X-Maktaba-Edge-Time") ?? "", signature: req.headers.get("X-Maktaba-Edge-Signature") ?? "" });
      const taken = [], duplicates = [];
      for (const w of JSON.parse(body).windows ?? []) {
        if (windows[w.key]) duplicates.push(w.key); else { windows[w.key] = w; taken.push(w.key); }
      }
      return Response.json({ taken, duplicates, rejected: [], states: [], plans: [] });
    }
    return new Response("not the cloud's", { status: 404 });
  },
};`;

const DO_CLASSES = {
  CHANNEL: "ChannelLive",
  PUBLISHER: "PublisherControl",
  REVOCATIONS: "Revocations",
  VIEWER: "ViewerAccess",
  PAIRING: "Pairing",
};

let vars = {
  CLOUD_PUBKEYS: vectors.cloud_pubkeys,
  VIEWER_TOKEN_KEY: VIEWER_KEY,
  BROADCAST_ENV: "rehearsal",
  BROADCAST_DELIVERY: "r2",
  BROADCAST_GLOBAL_OFF: "0",
  BROADCAST_OFF_CHANNELS: "",
  EDGE_VERSION: "1",
  TURNSTILE_SECRET,
  API_ORIGIN: CLOUD_ORIGIN,
  EDGE_SIGNING_KEY: EDGE_SEED,
};

function workerName(loc, v2) {
  return `edge-${loc}${v2 ? "-v2" : ""}`;
}

function options() {
  const workers = [
    {
      name: "router",
      modules: true,
      script: ROUTER,
      compatibilityDate: "2026-08-15",
      serviceBindings: {
        CLOUD: "cloud",
        ...Object.fromEntries(
          Array.from({ length: LOCATIONS }, (_, l) => [
            [`L${l}`, workerName(l, false)],
            [`L${l}_V2`, workerName(l, true)],
          ]).flat()
        ),
      },
    },
    { name: "turnstile", modules: true, script: TURNSTILE, compatibilityDate: "2026-08-15" },
    { name: "cloud", modules: true, script: CLOUD, compatibilityDate: "2026-08-15" },
  ];
  for (const v2 of [false, true]) {
    for (let l = 0; l < LOCATIONS; l++) {
      const owner = l === 0 && !v2;
      workers.push({
        name: workerName(l, v2),
        modules: [{ type: "ESModule", path: join(here, "entry.js"), contents: script }],
        compatibilityDate: "2026-08-15",
        bindings: { ...vars, ...(PLANT ? { REHEARSAL_PLANT: PLANT } : {}), EDGE_VERSION: v2 ? String(Number(vars.EDGE_VERSION) + 1) : vars.EDGE_VERSION },
        r2Buckets: { SEGMENTS: "segments", GUIDE: "guide" },
        serviceBindings: { TURNSTILE_API: "turnstile" },
        // every fetch out (the usage posts to API_ORIGIN) reaches the fake cloud
        outboundService: "cloud",
        // the Durable Objects live in location 0's first version; every other Worker binds them
        durableObjects: Object.fromEntries(
          Object.entries(DO_CLASSES).map(([b, cls]) => [
            b,
            owner ? cls : { className: cls, scriptName: workerName(0, false) },
          ])
        ),
        ratelimits: { RL: { namespace_id: "5021", simple: { limit: 60, period: 10 } } },
      });
    }
  }
  // Miniflare 5 takes its own config; its converter reads the familiar (v4) options
  return convertV4MiniflareOptions({
    host: "0.0.0.0",
    port: PORT,
    // the Durable Objects' and R2's state on disk, kept across a redeploy (edge-deploy)
    resourcePersistencePath: DATA,
    workers,
  });
}

mkdirSync(DATA, { recursive: true });
const mf = new Miniflare(options());
await mf.ready;
console.log(
  `broadcast edge rehearsal: ${LOCATIONS} locations on :${PORT}, control on :${CONTROL_PORT}${PLANT ? `, planted: ${PLANT}` : ""}`
);

let deploying = Promise.resolve();
createServer((req, res) => {
  const reply = (code, v) => {
    res.writeHead(code, { "Content-Type": "application/json" });
    res.end(JSON.stringify(v));
  };
  if (req.method === "GET" && req.url === "/healthz") return reply(200, { ok: true, vars, plant: PLANT });
  if (req.method === "POST" && req.url === "/deploy") {
    let body = "";
    req.on("data", (d) => (body += d));
    req.on("end", () => {
      let next;
      try {
        next = JSON.parse(body || "{}");
      } catch {
        return reply(400, { error: "bad_json" });
      }
      deploying = deploying.then(async () => {
        const t0 = Date.now();
        vars = { ...vars, ...(next.vars ?? {}) };
        await mf.setOptions(options());
        await mf.ready;
        console.log(`deployed in ${Date.now() - t0} ms: ${JSON.stringify(next.vars ?? {})}`);
        reply(200, { ok: true, ms: Date.now() - t0, vars });
      });
      deploying.catch((err) => reply(500, { error: String(err) }));
    });
    return;
  }
  reply(404, { error: "not_found" });
}).listen(CONTROL_PORT, "0.0.0.0");

for (const sig of ["SIGINT", "SIGTERM"])
  process.on(sig, async () => {
    await mf.dispose();
    process.exit(0);
  });
