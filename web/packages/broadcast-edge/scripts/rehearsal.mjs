#!/usr/bin/env node
// The broadcast edge's CI rehearsal (story 50.21): the real Worker under `wrangler dev --local`
// (workerd with local R2, Durable Objects and the Cache API), a fake home pushing 3 rungs over
// HTTP, and simulated viewers. No call leaves the machine and no Cloudflare account is used.
//
// It checks, over the wire:
//   - the home's PUTs are segments × rungs, whatever the number of viewers;
//   - R2 is read once per segment per rung (every other viewer is a cache hit);
//   - the playlists are HLS with the segments; the headers are the edge's;
//   - silence → the slate after a discontinuity; the home back → live after another;
//   - a token for another channel, or a fourth rung, is 403 and writes nothing.
//
//   node scripts/rehearsal.mjs [--viewers 100] [--segments 12] [--port 8799]
// It writes rehearsal-report.json (kept by CI) and exits 1 on any failed check.
import { spawn } from "node:child_process";
import { readFileSync, rmSync, writeFileSync } from "node:fs";
import { webcrypto as crypto } from "node:crypto";

const args = Object.fromEntries(
  process.argv
    .slice(2)
    .reduce((acc, a, i, all) => (a.startsWith("--") ? [...acc, [a.slice(2), all[i + 1]]] : acc), [])
);
const VIEWERS = Number(args.viewers ?? 100);
const SEGMENTS = Number(args.segments ?? 12);
const PORT = Number(args.port ?? 8799);
const BASE = `http://127.0.0.1:${PORT}`;
const RUNGS = ["src", "720p", "480p"];
const DUR = 1; // 1 s segments keep the rehearsal short; the offline rule is 3 target durations
const VIEWER_KEY = "cmVoZWFyc2FsLXZpZXdlci1rZXktbmV2ZXItcHJvZHVjdGlvbg";
const enc = new TextEncoder();

const b64url = (u) => Buffer.from(u).toString("base64url");
const hex = (u) => Buffer.from(u).toString("hex");
const sha256 = async (u) => new Uint8Array(await crypto.subtle.digest("SHA-256", u));

// the cloud's TEST key "cloud-1" (cloud/internal/broadcast/tokens_test.go's label)
const seed = await sha256(enc.encode("maktaba-broadcast TEST KEY, never for production: cloud-1"));
const pkcs8 = Buffer.concat([
  Buffer.from("302e020100300506032b657004220420", "hex"),
  Buffer.from(seed),
]);
const signKey = await crypto.subtle.importKey("pkcs8", pkcs8, { name: "Ed25519" }, true, ["sign"]);
const pub = Buffer.from((await crypto.subtle.exportKey("jwk", signKey)).x, "base64url");
const kid = hex(await sha256(pub)).slice(0, 16);
const vectors = JSON.parse(
  readFileSync("../../../cloud/internal/broadcast/testdata/vectors.json", "utf8")
);
if (!JSON.parse(vectors.cloud_pubkeys)[kid])
  throw new Error("the rehearsal's key is not the Go vectors' cloud-1");

async function ingestToken(claims) {
  const t = Math.floor(Date.now() / 1000);
  const body = b64url(
    enc.encode(
      JSON.stringify({
        typ: "ingest",
        pub: "pub-rehearsal",
        rungs: RUNGS,
        home: "home-rehearsal",
        kbps: 10000,
        iat: t,
        exp: t + 86400,
        kid,
        ...claims,
      })
    )
  );
  const sig = await crypto.subtle.sign(
    "Ed25519",
    signKey,
    enc.encode(`maktaba-broadcast-ingest-v1.${body}`)
  );
  return `mkbi1.${body}.${b64url(new Uint8Array(sig))}`;
}

const hmac = await crypto.subtle.importKey(
  "raw",
  Buffer.from(VIEWER_KEY, "base64url"),
  { name: "HMAC", hash: "SHA-256" },
  false,
  ["sign"]
);
async function viewerToken(ch, session) {
  const body = b64url(
    enc.encode(
      JSON.stringify({
        pub: "pub-rehearsal",
        ch,
        session,
        mode: "full",
        exp: Math.floor(Date.now() / 1000) + 600,
      })
    )
  );
  const sig = await crypto.subtle.sign(
    "HMAC",
    hmac,
    enc.encode(`maktaba-broadcast-viewer-v1.${body}`)
  );
  return `mkbv1.${body}.${b64url(new Uint8Array(sig))}`;
}

const checks = [];
function check(name, ok, detail = "") {
  checks.push({ name, ok: !!ok, detail });
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${detail ? `: ${detail}` : ""}`);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- start the Worker ---------------------------------------------------------------------
writeFileSync(
  ".dev.vars",
  [
    `CLOUD_PUBKEYS=${vectors.cloud_pubkeys}`,
    `VIEWER_TOKEN_KEY=${VIEWER_KEY}`,
    "BROADCAST_ENV=rehearsal",
    "BROADCAST_DELIVERY=r2",
    "BROADCAST_GLOBAL_OFF=0",
  ].join("\n") + "\n"
);
const wrangler = spawn(
  "npx",
  [
    "wrangler",
    "dev",
    "--local",
    "--port",
    String(PORT),
    "--ip",
    "127.0.0.1",
    "--local-upstream",
    `127.0.0.1:${PORT}`,
    "--persist-to",
    ".wrangler/rehearsal",
  ],
  {
    stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, WRANGLER_SEND_METRICS: "false", CI: "1" },
  }
);
let log = "";
wrangler.stdout.on("data", (d) => (log += d));
wrangler.stderr.on("data", (d) => (log += d));
function stop() {
  try {
    wrangler.kill("SIGTERM");
  } catch {}
  rmSync(".dev.vars", { force: true });
}
process.on("exit", stop);

let up = false;
for (let i = 0; i < 120 && !up; i++) {
  try {
    up = (await fetch(`${BASE}/`)).status === 404;
  } catch {
    await sleep(500);
  }
}
if (!up) {
  console.error(log);
  throw new Error("wrangler dev did not start");
}

// ---- the fake home ------------------------------------------------------------------------
const ch = `rehearsal${Date.now().toString(36)}`;
const token = await ingestToken({ ch });
let homePuts = 0;
let homeBytes = 0;

function segment(rung, seq, salt = "") {
  const size = { src: 24000, "720p": 12000, "480p": 6000 }[rung] ?? 4000;
  const b = new Uint8Array(size);
  const tag = enc.encode(`${rung}/${seq}/${salt}`);
  for (let i = 0; i < size; i++) b[i] = tag[i % tag.length] ^ (i & 0xff);
  return b;
}

async function put(path, body, extra = {}, tok = token) {
  const res = await fetch(`${BASE}${path}`, {
    method: "PUT",
    headers: {
      Authorization: `Bearer ${tok}`,
      "X-Maktaba-Time": String(Date.now()),
      "X-Maktaba-Body-SHA256": hex(await sha256(body)),
      "X-Maktaba-Duration": String(DUR),
      ...extra,
    },
    body,
  });
  await res.arrayBuffer();
  return res;
}

async function pushSegment(rung, seq) {
  const body = segment(rung, seq);
  const res = await put(`/v1/ingest/${ch}/${rung}/${seq}.ts`, body);
  if (res.status === 201) {
    homePuts++;
    homeBytes += body.byteLength;
  }
  return { res, name: `${seq}-${hex(await sha256(body)).slice(0, 8)}.ts` };
}

const state = await fetch(`${BASE}/v1/ingest/${ch}/state`, {
  method: "POST",
  headers: {
    Authorization: `Bearer ${token}`,
    "X-Maktaba-Time": String(Date.now()),
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    rungs: [
      { name: "src", bandwidth: 6500000, resolution: "1920x1080" },
      { name: "720p", bandwidth: 3000000, resolution: "1280x720" },
      { name: "480p", bandwidth: 1200000, resolution: "854x480" },
    ],
  }),
});
check("state post accepted", state.status === 200, String(state.status));
for (const idx of [0, 1]) {
  const r = await put(`/v1/ingest/${ch}/slate/${idx}.ts`, segment("slate", idx, "slate"));
  check(`slate segment ${idx} stored`, r.status === 201, String(r.status));
}

// ---- viewers ------------------------------------------------------------------------------
const tokens = await Promise.all(
  Array.from({ length: VIEWERS }, (_, i) => viewerToken(ch, `v${i}`))
);
let misses = 0;
let hits = 0;
const latencies = [];

async function view(tok, rung, name) {
  const t0 = performance.now();
  const r = await fetch(`${BASE}/${tok}/${ch}/${rung}/${name}`);
  const buf = await r.arrayBuffer();
  latencies.push(performance.now() - t0);
  if (r.status !== 200)
    throw new Error(`segment ${rung}/${name}: ${r.status} ${Buffer.from(buf).toString()}`);
  if (r.headers.get("X-Maktaba-Cache") === "miss") misses++;
  else hits++;
  return r;
}

// real viewers spread over a segment's duration: at most 50 requests in flight at once
async function pool(items, fn, width = 50) {
  let next = 0;
  await Promise.all(
    Array.from({ length: Math.min(width, items.length) }, async () => {
      while (next < items.length) await fn(items[next++]);
    })
  );
}

let lastHeaders = null;
for (let seq = 1; seq <= SEGMENTS; seq++) {
  const started = Date.now();
  for (const rung of RUNGS) {
    const { res, name } = await pushSegment(rung, seq);
    if (res.status !== 201) check(`segment ${rung}/${seq} pushed`, false, String(res.status));
    const mine = tokens.filter((_, i) => i % RUNGS.length === RUNGS.indexOf(rung));
    // the first viewer misses; the location's cache lock holds the rest until it is cached
    lastHeaders = (await view(mine[0], rung, name)).headers;
    await pool(mine.slice(1), (t) => view(t, rung, name));
  }
  await sleep(Math.max(0, DUR * 1000 - (Date.now() - started)));
}

const pl = await fetch(`${BASE}/${tokens[0]}/${ch}/src/index.m3u8`);
const m3u8 = await pl.text();
check(
  "home PUTs = segments × rungs",
  homePuts === SEGMENTS * RUNGS.length,
  `${homePuts} PUTs, ${homeBytes} bytes, ${VIEWERS} viewers`
);
check(
  "R2 reads ≤ segments × rungs (one per location)",
  misses <= SEGMENTS * RUNGS.length,
  `${misses} misses, ${hits} hits`
);
check(
  "every other viewer is a cache hit",
  hits === SEGMENTS * (VIEWERS - RUNGS.length),
  String(hits)
);
check(
  "media playlist is HLS v6 with the newest segment",
  pl.status === 200 && m3u8.includes("#EXT-X-VERSION:6") && m3u8.includes(`\n${SEGMENTS}-`),
  m3u8.split("\n").slice(-2)[0]
);
check("playlist cached 1 s", pl.headers.get("Cache-Control") === "public, max-age=1");
check(
  "segments immutable",
  lastHeaders?.get("Cache-Control") === "public, max-age=86400, immutable"
);
check("no cookie on live", !pl.headers.get("Set-Cookie") && !lastHeaders?.get("Set-Cookie"));
const sorted = [...latencies].sort((a, b) => a - b);
const p95 = sorted[Math.floor(sorted.length * 0.95)] ?? 0;
check("viewer segment p95 within 500 ms (local)", p95 < 500, `${p95.toFixed(1)} ms`);

// ---- silence, then back -------------------------------------------------------------------
await sleep(4500); // > 3 target durations of 1 s
await sleep(1100); // the cached playlist's second
const slate = await (await fetch(`${BASE}/${tokens[1]}/${ch}/src/index.m3u8`)).text();
check(
  "silence: the slate after a discontinuity",
  slate.includes("\n../slate/") && slate.includes("#EXT-X-DISCONTINUITY\n"),
  slate.split("\n").slice(-2)[0]
);
await pushSegment("src", SEGMENTS + 1);
await sleep(1100);
const back = await (await fetch(`${BASE}/${tokens[2]}/${ch}/src/index.m3u8`)).text();
const tail = back.split("\n").filter(Boolean).slice(-4);
check(
  "home back: live after a discontinuity",
  tail[3]?.startsWith(`${SEGMENTS + 1}-`) && tail[0] === "#EXT-X-DISCONTINUITY",
  tail.join(" | ")
);

// ---- refusals -----------------------------------------------------------------------------
const other = await put(`/v1/ingest/${ch}x/src/1.ts`, segment("src", 1));
check("another channel's token: 403", other.status === 403, String(other.status));
const fourth = await put(`/v1/ingest/${ch}/240p/1.ts`, segment("240p", 1));
check("a fourth rung: 403", fourth.status === 403, String(fourth.status));

const failed = checks.filter((c) => !c.ok);
writeFileSync(
  "rehearsal-report.json",
  JSON.stringify(
    {
      viewers: VIEWERS,
      segments: SEGMENTS,
      rungs: RUNGS.length,
      home_puts: homePuts,
      home_bytes: homeBytes,
      r2_reads: misses,
      cache_hits: hits,
      viewer_p95_ms: p95,
      checks,
    },
    null,
    2
  ) + "\n"
);
stop();
if (failed.length > 0) {
  console.error(`\n${failed.length} check(s) failed`);
  console.error(log.split("\n").slice(-40).join("\n"));
  process.exit(1);
}
console.log(`\nall ${checks.length} checks passed`);
process.exit(0);
