// Story 50.28 T01: the edge's Analytics Engine events. Each event has the columns of
// cloud/internal/broadcast/testdata/events_schema.json (the cloud's rollup reads them by
// position), and none carries an IP address, a pass id or an invite label.
import { afterEach, describe, expect, it } from "vitest";
import { runInDurableObject } from "cloudflare:test";
import { resetClock } from "../src/clock";
import type { Env } from "../src/env";
import * as ev from "../src/events";
import { call, E, LIVE, newChannel, putSegment, viewerToken } from "./helpers";

afterEach(() => resetClock());

interface Schema {
  index: string;
  blobs: string[];
  doubles: string[];
  events: Record<string, { blobs: string[]; doubles: string[] }>;
  devices: string[];
  blocked_reasons: string[];
}
const schema = JSON.parse(
  (E as unknown as { TEST_EVENTS_SCHEMA: string }).TEST_EVENTS_SCHEMA
) as Schema;

/** fakeAE collects the points written. */
function fakeAE(): { points: ev.DataPoint[]; AE: AnalyticsEngineDataset } {
  const points: ev.DataPoint[] = [];
  return {
    points,
    AE: { writeDataPoint: (p?: AnalyticsEngineDataPoint) => points.push(p as ev.DataPoint) },
  };
}

/** named reads a point back by the schema's column names. */
function named(p: ev.DataPoint): Record<string, string | number> {
  const out: Record<string, string | number> = {};
  schema.blobs.forEach((k, i) => (out[k] = p.blobs[i]!));
  schema.doubles.forEach((k, i) => (out[k] = p.doubles[i]!));
  return out;
}

const viewer: ev.ViewerContext = {
  pub: "pub-1",
  ch: "chan1",
  country: "EG",
  device: "tvos",
  access: "open",
  mode: "full",
  session: "hashed-session",
};

describe("T01 the event writer", () => {
  it("has the schema's columns, in its order", () => {
    expect([...ev.BLOBS]).toEqual(schema.blobs);
    expect([...ev.DOUBLES]).toEqual(schema.doubles);
    expect([...ev.DEVICES]).toEqual(schema.devices);
  });

  const cases: [string, ev.DataPoint][] = [
    ["play", ev.play(viewer, 850)],
    ["beat", ev.beat(viewer, "720p", 1200, 3000)],
    ["switch", ev.rungSwitch("pub-1", "chan1", "720p", "480p", 300)],
    ["error", ev.error("pub-1", "chan1", "storage_unavailable")],
    ["blocked", ev.blocked("pub-1", "chan1", "full")],
    ["ingest", ev.ingest("pub-1", "chan1", "src", 2048, 900, 7000)],
  ];
  for (const [name, p] of cases) {
    it(`writes ${name} with its fields and nothing else`, () => {
      const want = schema.events[name]!;
      const got = named(p);
      expect(p.indexes).toEqual(["pub-1"]);
      expect(got.event).toBe(name);
      for (const k of schema.blobs) {
        if (k === "event") continue;
        if (want.blobs.includes(k)) expect(got[k], `${name}.${k}`).not.toBe("");
        else expect(got[k], `${name}.${k} unused`).toBe("");
      }
      for (const k of schema.doubles) {
        if (!want.doubles.includes(k)) expect(got[k], `${name}.${k} unused`).toBe(0);
      }
    });
  }

  it("writes the numbers given, and minutes = 1 per beat", () => {
    expect(named(ev.play(viewer, 850)).startup_ms).toBe(850);
    const b = named(ev.beat(viewer, "720p", 1200, 3000));
    expect([b.minutes, b.rebuffer_ms, b.bitrate_kbps]).toEqual([1, 1200, 3000]);
    const i = named(ev.ingest("pub-1", "chan1", "src", 2048, 900));
    expect([i.segment_bytes, i.upload_to_visible_ms, i.glass_to_glass_ms]).toEqual([2048, 900, 0]);
    // a nonsense number is 0, never NaN or negative
    expect(named(ev.play(viewer, Number.NaN)).startup_ms).toBe(0);
    expect(named(ev.play(viewer, -5)).startup_ms).toBe(0);
  });

  it("hashes the session per day, never writing it in the clear", async () => {
    const key = E.VIEWER_TOKEN_KEY;
    const day1 = Date.UTC(2026, 10, 14, 10);
    const a = await ev.sessionHash(key, "session-abc", day1);
    expect(a).not.toContain("session-abc");
    expect(a).toMatch(/^[A-Za-z0-9_-]{16}$/);
    expect(await ev.sessionHash(key, "session-abc", day1 + 3600_000)).toBe(a);
    expect(await ev.sessionHash(key, "session-abd", day1)).not.toBe(a);
    expect(await ev.sessionHash(key, "session-abc", day1 + 86400_000)).not.toBe(a);
  });

  it("names the device class from the player's word, else the User-Agent", () => {
    expect(ev.deviceClass("androidtv", null)).toBe("androidtv");
    expect(ev.deviceClass("nonsense", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0)")).toBe("ios");
    expect(ev.deviceClass(null, "AppleTV14,1/tvOS 18")).toBe("tvos");
    expect(ev.deviceClass(null, "Mozilla/5.0 (Linux; Android 14; BRAVIA 4K)")).toBe("androidtv");
    expect(ev.deviceClass(null, "Mozilla/5.0 (Linux; Android 14; Pixel 8)")).toBe("android");
    expect(ev.deviceClass(null, "Mozilla/5.0 Electron/30")).toBe("desktop");
    expect(ev.deviceClass(null, "Mozilla/5.0 (Macintosh) Safari")).toBe("web");
  });
});

describe("T01 the events a viewer's requests write", () => {
  const ip = "203.0.113.7";
  const forbidden = (points: ev.DataPoint[], token: string) => {
    const text = JSON.stringify(points);
    expect(text).not.toContain(ip);
    expect(text).not.toContain(token);
    expect(text).not.toContain("invite-label");
    expect(text).not.toContain("pass-");
  };

  it("a beat writes play once, then a beat a minute, with the player's report", async () => {
    const ch = newChannel();
    const session = "pass-xyz-session";
    const tok = await viewerToken({ ch, session, country: "EG", acc: "open" });
    const { points, AE } = fakeAE();
    const report = {
      rung: "720p",
      rebuffer_ms: 400,
      kbps: 3000,
      startup_ms: 900,
      device: "tvos",
      switches: [{ from: "src", to: "720p", ms: 250 }],
      errors: ["decode_failed", "Not A Code!"],
      label: "invite-label",
    };
    const headers = { "CF-Connecting-IP": ip, "Content-Type": "application/json" };
    const r = await call(
      `${LIVE}/${tok}/${ch}/beat`,
      { method: "POST", headers, body: JSON.stringify(report) },
      { AE }
    );
    expect(r.status).toBe(200);
    expect(await r.json()).toMatchObject({ counted: true });
    const kinds = points.map((p) => p.blobs[0]);
    expect(kinds).toEqual(["play", "beat", "switch", "error"]);
    const play = named(points[0]!);
    expect(play).toMatchObject({
      publisher: "pub-1",
      channel: ch,
      country: "EG",
      device: "tvos",
      access: "open",
      mode: "full",
      startup_ms: 900,
    });
    expect(play.session).not.toBe(session);
    expect(named(points[1]!)).toMatchObject({ rung: "720p", minutes: 1, rebuffer_ms: 400 });
    expect(named(points[2]!)).toMatchObject({ from: "src", to: "720p", switch_ms: 250 });
    expect(named(points[3]!).code).toBe("decode_failed");
    forbidden(points, tok);

    // a second beat within the minute counts nothing and writes nothing, its report included
    points.length = 0;
    await call(
      `${LIVE}/${tok}/${ch}/beat`,
      { method: "POST", headers, body: JSON.stringify({ ...report, errors: ["again"] }) },
      { AE }
    );
    expect(points).toEqual([]);
    // a body over 4 KB is ignored, the beat still answers
    const big = await call(
      `${LIVE}/${await viewerToken({ ch })}/${ch}/beat`,
      { method: "POST", headers, body: JSON.stringify({ rung: "x".repeat(5000) }) },
      { AE }
    );
    expect(big.status).toBe(200);
    expect(named(points[1]!).rung).toBe("");
  });

  it("a full channel writes blocked full; a revoked one blocked revoked", async () => {
    const ch = newChannel();
    const { points, AE } = fakeAE();
    const wait = await viewerToken({ ch, mode: "wait" });
    expect((await call(`${LIVE}/${wait}/${ch}/master.m3u8`, {}, { AE })).status).toBe(429);
    expect(named(points[0]!)).toMatchObject({ event: "blocked", code: "full", channel: ch });

    await putSegment(ch, "src", 1);
    await (E as Env).CHANNEL.get((E as Env).CHANNEL.idFromName(ch)).revoke("takedown");
    points.length = 0;
    const tok = await viewerToken({ ch });
    expect((await call(`${LIVE}/${tok}/${ch}/master.m3u8`, {}, { AE })).status).toBe(403);
    expect(named(points[0]!)).toMatchObject({ event: "blocked", code: "revoked" });
    // the media playlists of the same session add no second blocked
    points.length = 0;
    await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`, {}, { AE });
    expect(points).toEqual([]);
  });

  it("a segment made visible writes ingest, glass-to-glass only for the canary", async () => {
    const ch = newChannel();
    const stub = (E as Env).CHANNEL.get((E as Env).CHANNEL.idFromName(ch));
    const { points, AE } = fakeAE();
    await runInDurableObject(stub, (instance) => {
      const env = (instance as unknown as { env: Env }).env;
      env.AE = AE;
      env.CANARY_PUBLISHER = "pub-1";
    });
    expect((await putSegment(ch, "src", 1)).status).toBe(201);
    // seq 3 waits for 2: visible only when 2 arrives, then both are written
    expect((await putSegment(ch, "src", 3)).status).toBe(201);
    expect(points).toHaveLength(1);
    expect((await putSegment(ch, "src", 2)).status).toBe(201);
    expect(points.map((p) => named(p).event)).toEqual(["ingest", "ingest", "ingest"]);
    const first = named(points[0]!);
    expect(first).toMatchObject({
      publisher: "pub-1",
      channel: ch,
      rung: "src",
      segment_bytes: 2048,
    });
    expect(first.glass_to_glass_ms as number).toBeGreaterThan(0);
  });
});
