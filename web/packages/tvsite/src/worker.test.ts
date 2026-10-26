// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import parity from "./testdata/parity.json";
import { HEADER_SIGNATURE, HEADER_TIME, signHome } from "./homeauth";
import worker, { HOME_SILENT_SECONDS, SiteControl, type Env, type SiteState } from "./worker";

const SECRET = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
const enc = new TextEncoder();

// a Durable Object namespace with one real SiteControl over in-memory storage
function fakeEnv(over: Partial<Env> = {}): Env {
  const mem = new Map<string, unknown>();
  const obj = new SiteControl({
    storage: {
      get: async <T>(k: string) => mem.get(k) as T | undefined,
      put: async <T>(k: string, v: T) => void mem.set(k, structuredClone(v)),
    },
  });
  return {
    SITE: {
      idFromName: (n: string) => n,
      get: () => ({
        fetch: (input: Request | string, init?: RequestInit) => obj.fetch(new Request(input, init)),
      }),
    },
    ASSETS: {
      fetch: async () =>
        new Response("<!doctype html>site", { headers: { "Content-Type": "text/html" } }),
    },
    HOME_HMAC: SECRET,
    STREAM_KEY_ID: parity.kid,
    STREAM_KEY_JWK: parity.jwk,
    ...over,
  };
}

const state = (over: Partial<SiteState> = {}): SiteState => ({
  schema: 1,
  at: new Date().toISOString(),
  customer_code: "abc123xyz",
  channels: [
    { slug: "news", number: 101, name: "News", on_air: true, video_uid: "vid-live-1" },
    { slug: "films", number: 102, name: "Films", on_air: false },
  ],
  ...over,
});

async function homePost(
  env: Env,
  body: unknown,
  opts: { ts?: number; secret?: string } = {}
): Promise<Response> {
  const raw = enc.encode(JSON.stringify(body));
  const ts = opts.ts ?? Math.floor(Date.now() / 1000);
  const sig = await signHome(opts.secret ?? SECRET, "POST", "/_home/state", ts, raw);
  return worker.fetch(
    new Request("https://tv.example.com/_home/state", {
      method: "POST",
      body: raw,
      headers: { [HEADER_TIME]: String(ts), [HEADER_SIGNATURE]: sig },
    }),
    env
  );
}

const get = (env: Env, path: string) =>
  worker.fetch(new Request("https://tv.example.com" + path), env);
const play = (env: Env, slug: string) =>
  worker.fetch(
    new Request("https://tv.example.com/api/v1/play", {
      method: "POST",
      body: JSON.stringify({ slug }),
    }),
    env
  );

function b64urlBytes(s: string): Uint8Array {
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

async function verifies(token: string): Promise<boolean> {
  const jwk = JSON.parse(atob(parity.jwk)) as JsonWebKey;
  const pub = await crypto.subtle.importKey(
    "jwk",
    { kty: "RSA", n: jwk.n, e: jwk.e, alg: "RS256" },
    { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" },
    false,
    ["verify"]
  );
  const [h, c, s] = token.split(".");
  return crypto.subtle.verify("RSASSA-PKCS1-v1_5", pub, b64urlBytes(s), enc.encode(`${h}.${c}`));
}

describe("the home's post", () => {
  it("is refused without the right signature, a fresh time, or once replayed", async () => {
    const env = fakeEnv();
    expect((await homePost(env, state(), { secret: "wrong" })).status).toBe(401);
    expect((await homePost(env, state(), { ts: Math.floor(Date.now() / 1000) - 120 })).status).toBe(
      401
    );
    const ts = Math.floor(Date.now() / 1000);
    expect((await homePost(env, state(), { ts })).status).toBe(204);
    expect((await homePost(env, state(), { ts })).status).toBe(409);
    expect((await homePost(env, { schema: 2 }, { ts: ts + 1 })).status).toBe(400);
    expect((await homePost(fakeEnv({ HOME_HMAC: "" }), state())).status).toBe(401);
  });
});

describe("the public API", () => {
  beforeEach(() => vi.useFakeTimers({ now: new Date("2026-10-26T12:00:00Z"), toFake: ["Date"] }));
  afterEach(() => vi.useRealTimers());

  it("lists the lineup without video ids", async () => {
    const env = fakeEnv();
    await homePost(env, state());
    const r = await get(env, "/api/v1/lineup");
    const body = await r.text();
    expect(r.status).toBe(200);
    expect(r.headers.get("Cache-Control")).toBe("no-store");
    expect(body).not.toContain("vid-live-1");
    expect(JSON.parse(body).channels).toEqual([
      { slug: "news", number: 101, name: "News", on_air: true },
      { slug: "films", number: 102, name: "Films", on_air: false },
    ]);
  });

  it("plays a live channel with a token for its video, signed with the site key", async () => {
    const env = fakeEnv();
    await homePost(env, state());
    const r = await play(env, "news");
    expect(r.status).toBe(200);
    const { url, exp, refresh_after } = (await r.json()) as {
      url: string;
      exp: number;
      refresh_after: number;
    };
    const m = url.match(
      /^https:\/\/customer-abc123xyz\.cloudflarestream\.com\/([^/]+)\/manifest\/video\.m3u8$/
    );
    expect(m).not.toBeNull();
    const token = m![1];
    expect(await verifies(token)).toBe(true);
    const claims = JSON.parse(new TextDecoder().decode(b64urlBytes(token.split(".")[1])));
    expect(claims.sub).toBe("vid-live-1");
    expect(claims.kid).toBe(parity.kid);
    expect(exp - Date.now() / 1000).toBe(600);
    expect(refresh_after).toBe(300);
  });

  it("answers off air for a channel not live, or when the home went silent", async () => {
    const env = fakeEnv({ STREAM_BASE_URL: "http://127.0.0.1:9999/" });
    await homePost(env, state());
    expect((await play(env, "films")).status).toBe(503);
    expect((await play(env, "nope")).status).toBe(404);
    const ok = (await (await play(env, "news")).json()) as { url: string };
    expect(ok.url.startsWith("http://127.0.0.1:9999/")).toBe(true);
    vi.setSystemTime(Date.now() + (HOME_SILENT_SECONDS + 1) * 1000);
    const r = await play(env, "news");
    expect(r.status).toBe(503);
    expect(((await r.json()) as { code: string }).code).toBe("off_air");
    const l = (await (await get(env, "/api/v1/lineup")).json()) as {
      channels: { on_air: boolean }[];
    };
    expect(l.channels.every((c) => !c.on_air)).toBe(true);
  });

  it("serves the static site for other paths and 404 for unknown API routes", async () => {
    const env = fakeEnv();
    expect(await (await get(env, "/")).text()).toContain("site");
    expect((await get(env, "/api/v1/nope")).status).toBe(404);
    expect((await get(env, "/_home/state")).status).toBe(405);
    expect((await play(env, "news")).status).toBe(404); // nothing posted yet
  });
});
