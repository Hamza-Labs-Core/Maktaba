// Test helpers for the broadcast edge in Miniflare: the cloud's TEST keys (the same public labels
// as cloud/internal/broadcast/tokens_test.go), token minting, and a fake home's uploads.
import { env } from "cloudflare:workers";
import { b64url, hex, importEd25519Seed, utf8 } from "../src/crypto";
import type { Env } from "../src/env";
import {
  CONTROL_DOMAIN,
  CONTROL_PREFIX,
  INGEST_DOMAIN,
  INGEST_PREFIX,
  mintViewerToken,
  type IngestClaims,
  type ViewerClaims,
} from "../src/tokens";
import worker from "../src/worker";
import { now } from "../src/clock";

export const E = env as unknown as Env & { TEST_VECTORS: string; TEST_INVITE_VECTORS: string };
export const INGEST = "https://ingest.maktaba.tv";
export const LIVE = "https://live.maktaba.tv";

async function seedOf(label: string): Promise<Uint8Array> {
  return new Uint8Array(
    await crypto.subtle.digest(
      "SHA-256",
      utf8(`maktaba-broadcast TEST KEY, never for production: ${label}`)
    )
  );
}

/** The kid of the cloud's TEST key "cloud-1" (as Go's KidFor names it). */
export async function cloudKid(label = "cloud-1"): Promise<string> {
  const key = await importEd25519Seed(await seedOf(label));
  const jwk = (await crypto.subtle.exportKey("jwk", key)) as JsonWebKey;
  const pub = Uint8Array.from(atob((jwk.x ?? "").replace(/-/g, "+").replace(/_/g, "/")), (c) =>
    c.charCodeAt(0)
  );
  return hex(await crypto.subtle.digest("SHA-256", pub)).slice(0, 16);
}

export async function signCloud(
  prefix: string,
  domain: string,
  payload: unknown,
  label = "cloud-1"
): Promise<string> {
  const body = b64url(utf8(JSON.stringify(payload)));
  const key = await importEd25519Seed(await seedOf(label));
  const sig = await crypto.subtle.sign("Ed25519", key, utf8(`${domain}.${body}`));
  return `${prefix}${body}.${b64url(new Uint8Array(sig))}`;
}

let n = 0;
/** A fresh channel id per test, so Durable Object state never leaks between tests. */
export function newChannel(): string {
  n++;
  return `ch${Date.now().toString(36)}${n}`;
}

export const RUNGS = ["src", "720p", "480p"];

export async function ingestToken(
  over: Partial<IngestClaims> & { ch: string },
  label = "cloud-1"
): Promise<string> {
  const t = Math.floor(now() / 1000);
  const claims: IngestClaims = {
    typ: "ingest",
    pub: "pub-1",
    rungs: RUNGS,
    home: "home-1",
    kbps: 10000,
    iat: t,
    exp: t + 24 * 3600,
    kid: await cloudKid(label),
    ...over,
  };
  return signCloud(INGEST_PREFIX, INGEST_DOMAIN, claims, label);
}

export async function control(
  typ: string,
  payload: Record<string, unknown>,
  ttlS = 900
): Promise<string> {
  const t = Math.floor(now() / 1000);
  return signCloud(CONTROL_PREFIX, CONTROL_DOMAIN, {
    ...payload,
    typ,
    kid: await cloudKid(),
    iat: t,
    exp: t + ttlS,
  });
}

export async function viewerToken(over: Partial<ViewerClaims> & { ch: string }): Promise<string> {
  return mintViewerToken(E.VIEWER_TOKEN_KEY, {
    pub: "pub-1",
    session: `s-${Math.random().toString(36).slice(2)}`,
    mode: "full",
    exp: Math.floor(now() / 1000) + 600,
    ...over,
  });
}

/** call sends a request through the Worker (with an env override when given). */
export function call(
  input: string,
  init?: RequestInit,
  envOver: Partial<Env> = {}
): Promise<Response> {
  // the coarse per-location limiters are off unless a test brings one: the exact guards
  // (ChannelLive's per rung, ViewerAccess's) are what the tests measure, on a clock they move
  return worker.fetch(new Request(input, init), {
    ...E,
    RL: undefined,
    PLAY_RL: undefined,
    API_RL: undefined,
    ...envOver,
  } as Env);
}

/** segmentBody is a fake TS segment of `size` bytes, different for each (rung, seq, salt). */
export function segmentBody(rung: string, seq: number, size = 2048, salt = ""): Uint8Array {
  const b = new Uint8Array(size);
  const tag = utf8(`${rung}/${seq}/${salt}`);
  for (let i = 0; i < size; i++) b[i] = tag[i % tag.length]! ^ (i & 0xff);
  return b;
}

export interface PutOpts {
  token?: string;
  dur?: number;
  disc?: boolean;
  time?: number;
  sha?: string;
  body?: Uint8Array;
  salt?: string;
}

export async function putSegment(
  ch: string,
  rung: string,
  seq: number,
  o: PutOpts = {}
): Promise<Response> {
  const body = o.body ?? segmentBody(rung, seq, 2048, o.salt);
  const sha = o.sha ?? hex(await crypto.subtle.digest("SHA-256", body));
  const headers: Record<string, string> = {
    Authorization: `Bearer ${o.token ?? (await ingestToken({ ch }))}`,
    "X-Maktaba-Time": String(o.time ?? now()),
    "X-Maktaba-Body-SHA256": sha,
    "X-Maktaba-Duration": String(o.dur ?? 4),
    "Content-Length": String(body.byteLength),
  };
  if (o.disc) headers["X-Maktaba-Discontinuity"] = "1";
  return call(`${INGEST}/v1/ingest/${ch}/${rung}/${seq}.ts`, { method: "PUT", headers, body });
}

export async function putSlate(ch: string, idx: number, dur = 4): Promise<Response> {
  return putSegment(ch, "slate", idx, { dur, salt: "slate" });
}

export async function postState(ch: string, body: unknown, token?: string): Promise<Response> {
  return call(`${INGEST}/v1/ingest/${ch}/state`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token ?? (await ingestToken({ ch }))}`,
      "X-Maktaba-Time": String(now()),
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
}

export const STATE_3 = {
  rungs: [
    { name: "src", bandwidth: 6_500_000, resolution: "1920x1080", codecs: "avc1.640028,mp4a.40.2" },
    { name: "720p", bandwidth: 3_000_000, resolution: "1280x720" },
    { name: "480p", bandwidth: 1_200_000, resolution: "854x480" },
  ],
};

/** segmentURIs lists the URIs a media playlist names. */
export function uris(m3u8: string): string[] {
  return m3u8.split("\n").filter((l) => l && !l.startsWith("#"));
}
