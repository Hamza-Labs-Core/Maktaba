// T01: the ingest token at the edge (good / expired / wrong channel / wrong rung / bad signature /
// replayed seq → accept / 401 / 403 / 403 / 401 / 409), the Go vectors on both sides, and the
// viewer token.
import { afterEach, describe, expect, it } from "vitest";
import { fromB64url, sha256Hex, utf8 } from "../src/crypto";
import { signEdgeRequest } from "../src/edge_sign";
import { resetClock } from "../src/clock";
import { mintViewerToken, verifyControl, verifyIngest, verifyViewer } from "../src/tokens";
import { E, ingestToken, newChannel, putSegment, viewerToken } from "./helpers";

const V = JSON.parse(E.TEST_VECTORS) as {
  at_ms: number;
  cloud_pubkeys: string;
  ingest: string;
  ingest_claims: { ch: string; rungs: string[]; pub: string };
  plan: string;
  revocations: string;
  edge: {
    seed: string;
    public: string;
    method: string;
    path: string;
    ms: number;
    body: string;
    signature: string;
  };
};

afterEach(() => resetClock());

describe("T01 ingest token at the edge", () => {
  it("accepts a good token", async () => {
    const ch = newChannel();
    expect((await putSegment(ch, "src", 1)).status).toBe(201);
  });

  it("refuses an expired token with 401", async () => {
    const ch = newChannel();
    const t = Math.floor(Date.now() / 1000);
    const r = await putSegment(ch, "src", 1, {
      token: await ingestToken({ ch, iat: t - 90_000, exp: t - 10 }),
    });
    expect(r.status).toBe(401);
    expect(await r.json()).toEqual({ error: "expired" });
  });

  it("refuses a token for another channel with 403, writing nothing", async () => {
    const ch = newChannel();
    const other = newChannel();
    const r = await putSegment(other, "src", 1, { token: await ingestToken({ ch }) });
    expect(r.status).toBe(403);
    expect(await r.json()).toEqual({ error: "wrong_channel" });
    expect((await E.SEGMENTS.list({ prefix: `test/${other}/` })).objects).toHaveLength(0);
  });

  it("refuses a fourth rung with 403, writing nothing", async () => {
    const ch = newChannel();
    const r = await putSegment(ch, "240p", 1);
    expect(r.status).toBe(403);
    expect(await r.json()).toEqual({ error: "wrong_rung" });
    expect((await E.SEGMENTS.list({ prefix: `test/${ch}/` })).objects).toHaveLength(0);
  });

  it("refuses a bad signature with 401", async () => {
    const ch = newChannel();
    // signed by a key the edge does not hold under that kid
    const forged = await ingestToken({ ch }, "cloud-forged");
    expect((await putSegment(ch, "src", 1, { token: forged })).status).toBe(401);
    // a good token with one changed payload byte
    const good = await ingestToken({ ch });
    const [p, body, sig] = [
      good.slice(0, 6),
      good.slice(6, good.lastIndexOf(".")),
      good.slice(good.lastIndexOf(".") + 1),
    ];
    const claims = JSON.parse(new TextDecoder().decode(fromB64url(body))) as Record<
      string,
      unknown
    >;
    claims.kbps = 99999;
    const tampered = `${p}${btoa(JSON.stringify(claims)).replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_")}.${sig}`;
    const r = await putSegment(ch, "src", 1, { token: tampered });
    expect(r.status).toBe(401);
    expect(await r.json()).toEqual({ error: "bad_signature" });
  });

  it("answers a replayed seq with a different body 409, the same body 200", async () => {
    const ch = newChannel();
    expect((await putSegment(ch, "src", 5)).status).toBe(201);
    expect((await putSegment(ch, "src", 5)).status).toBe(200);
    const r = await putSegment(ch, "src", 5, { salt: "another body" });
    expect(r.status).toBe(409);
    expect((await E.SEGMENTS.list({ prefix: `test/${ch}/src/` })).objects).toHaveLength(1);
  });

  it("refuses a home clock more than 60 s off, telling it the edge's time", async () => {
    const ch = newChannel();
    const r = await putSegment(ch, "src", 1, { time: Date.now() - 61_000 });
    expect(r.status).toBe(400);
    expect(await r.json()).toEqual({ error: "clock_skew" });
    expect(Number(r.headers.get("X-Maktaba-Edge-Now"))).toBeGreaterThan(0);
  });

  it("refuses a body whose SHA-256 is not the header's", async () => {
    const ch = newChannel();
    const r = await putSegment(ch, "src", 1, { sha: "0".repeat(64) });
    expect(r.status).toBe(400);
    expect(await r.json()).toEqual({ error: "sha256_mismatch" });
  });
});

describe("vectors shared with the cloud (cloud/internal/broadcast)", () => {
  it("verifies the Go-signed ingest token", async () => {
    const v = await verifyIngest(V.ingest, V.cloud_pubkeys, V.at_ms + 1000, {
      ch: V.ingest_claims.ch,
      rung: "720p",
    });
    expect(v.ok).toBe(true);
    if (v.ok)
      expect(v.claims).toMatchObject({ pub: "pub-1", rungs: ["src", "720p", "480p"], kbps: 10000 });
    expect(
      await verifyIngest(V.ingest, V.cloud_pubkeys, V.at_ms + 24 * 3600_000, {
        ch: V.ingest_claims.ch,
      })
    ).toMatchObject({ ok: false, status: 401, error: "expired" });
  });

  it("verifies the Go-signed control posts, each only as its own type", async () => {
    expect((await verifyControl(V.plan, "plan", V.cloud_pubkeys, V.at_ms)).ok).toBe(true);
    expect((await verifyControl(V.revocations, "revocations", V.cloud_pubkeys, V.at_ms)).ok).toBe(
      true
    );
    expect(await verifyControl(V.plan, "revocations", V.cloud_pubkeys, V.at_ms)).toEqual({
      ok: false,
      error: "wrong_type",
    });
    // an ingest token is never a control post, and the reverse
    expect((await verifyControl(V.ingest, "plan", V.cloud_pubkeys, V.at_ms)).ok).toBe(false);
    expect((await verifyIngest("mkbi1." + V.plan.slice(6), V.cloud_pubkeys, V.at_ms, {})).ok).toBe(
      false
    );
  });

  it("signs the edge's post byte for byte as Go does", async () => {
    const h = await signEdgeRequest(
      V.edge.seed,
      V.edge.method,
      V.edge.path,
      V.edge.ms,
      utf8(V.edge.body)
    );
    expect(h["X-Maktaba-Edge-Signature"]).toBe(V.edge.signature);
    expect(h["X-Maktaba-Edge-Time"]).toBe(String(V.edge.ms));
    expect(await sha256Hex("")).toHaveLength(64);
  });
});

describe("viewer tokens", () => {
  it("accepts a good token, refuses another channel, an expired or an over-long one", async () => {
    const ch = newChannel();
    const t = Date.now();
    const good = await viewerToken({ ch });
    expect((await verifyViewer(good, E.VIEWER_TOKEN_KEY, t, ch)).ok).toBe(true);
    expect(await verifyViewer(good, E.VIEWER_TOKEN_KEY, t, "other")).toMatchObject({
      status: 403,
      error: "wrong_channel",
    });
    const old = await viewerToken({ ch, exp: Math.floor(t / 1000) - 1 });
    expect(await verifyViewer(old, E.VIEWER_TOKEN_KEY, t, ch)).toMatchObject({
      status: 401,
      error: "expired",
    });
    const long = await viewerToken({ ch, exp: Math.floor(t / 1000) + 3600 });
    expect(await verifyViewer(long, E.VIEWER_TOKEN_KEY, t, ch)).toMatchObject({
      status: 401,
      error: "bad_lifetime",
    });
    const otherKey = await mintViewerToken("b3RoZXIta2V5LW90aGVyLWtleS1vdGhlci1rZXktMDA", {
      pub: "p",
      ch,
      session: "s",
      mode: "full",
      exp: Math.floor(t / 1000) + 60,
    });
    expect(await verifyViewer(otherKey, E.VIEWER_TOKEN_KEY, t, ch)).toMatchObject({
      status: 401,
      error: "bad_signature",
    });
  });
});
