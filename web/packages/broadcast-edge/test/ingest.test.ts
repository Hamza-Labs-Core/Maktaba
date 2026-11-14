// T05 (the size and rate guards), the uplink test, the state post, the slate, the control socket,
// R2's absence, and the cloud's control posts: a plan (50.20) and the revocation list (50.25).
import { afterEach, describe, expect, it } from "vitest";
import { runInDurableObject } from "cloudflare:test";
import { advanceClock, resetClock } from "../src/clock";
import { forgetRevocations } from "../src/revocations";
import type { Env } from "../src/env";
import {
  call,
  control,
  E,
  INGEST,
  ingestToken,
  LIVE,
  newChannel,
  postState,
  putSegment,
  putSlate,
  segmentBody,
  STATE_3,
  viewerToken,
} from "./helpers";
import { now } from "../src/clock";

afterEach(() => {
  resetClock();
  forgetRevocations();
});

describe("T05 size and rate guards", () => {
  it("refuses a segment bigger than duration × top kbps × 1.5 with 413, writing nothing", async () => {
    const ch = newChannel();
    // 4 s at 100 kbps: at most 75,000 bytes
    const token = await ingestToken({ ch, kbps: 100 });
    expect(
      (await putSegment(ch, "src", 1, { token, body: segmentBody("src", 1, 75_000) })).status
    ).toBe(201);
    const r = await putSegment(ch, "src", 2, { token, body: segmentBody("src", 2, 75_001) });
    expect(r.status).toBe(413);
    expect((await E.SEGMENTS.list({ prefix: `test/${ch}/src/2-` })).objects).toHaveLength(0);
  });

  it("allows 4 PUTs a second per channel and rung, then 429", async () => {
    const ch = newChannel();
    const token = await ingestToken({ ch });
    const codes: number[] = [];
    for (let s = 1; s <= 10; s++) codes.push((await putSegment(ch, "src", s, { token })).status);
    expect(codes.filter((c) => c === 201)).toHaveLength(4);
    expect(codes.filter((c) => c === 429)).toHaveLength(6);
    // another rung has its own budget
    expect((await putSegment(ch, "720p", 1, { token })).status).toBe(201);
    // the coarse per-location limiter (the RL binding) refuses too
    const refuse = { limit: async () => ({ success: false }) };
    const body = segmentBody("src", 99);
    const sha = [...new Uint8Array(await crypto.subtle.digest("SHA-256", body))]
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const limited = await call(
      `${INGEST}/v1/ingest/${ch}/480p/1.ts`,
      {
        method: "PUT",
        headers: {
          Authorization: `Bearer ${token}`,
          "X-Maktaba-Time": String(now()),
          "X-Maktaba-Body-SHA256": sha,
          "X-Maktaba-Duration": "4",
        },
        body,
      },
      { RL: refuse }
    );
    expect(limited.status).toBe(429);
    // a second later the rung takes segments again
    advanceClock(1100);
    expect((await putSegment(ch, "src", 5, { token })).status).toBe(201);
  });
});

describe("ingest", () => {
  it("measures the uplink and writes nothing", async () => {
    const ch = newChannel();
    const body = new Uint8Array(512 * 1024);
    const r = await call(`${INGEST}/v1/ingest/speedtest`, {
      method: "PUT",
      headers: {
        Authorization: `Bearer ${await ingestToken({ ch })}`,
        "X-Maktaba-Time": String(now()),
      },
      body,
    });
    expect(r.status).toBe(200);
    const j = (await r.json()) as { bytes: number; kbps: number };
    expect(j.bytes).toBe(body.byteLength);
    expect(j.kbps).toBeGreaterThan(0);
    expect((await E.SEGMENTS.list({ prefix: `test/${ch}/` })).objects).toHaveLength(0);
  });

  it("writes each segment under <env>/<channel>/<rung>/<seq>-<hash8>.ts and lists it", async () => {
    const ch = newChannel();
    for (const rung of ["src", "720p", "480p"])
      expect((await putSegment(ch, rung, 100)).status).toBe(201);
    const keys = (await E.SEGMENTS.list({ prefix: `test/${ch}/` })).objects
      .map((o) => o.key)
      .sort();
    expect(keys).toHaveLength(3);
    for (const k of keys)
      expect(k).toMatch(new RegExp(`^test/${ch}/(src|720p|480p)/100-[0-9a-f]{8}\\.ts$`));
    const tok = await viewerToken({ ch });
    for (const rung of ["src", "720p", "480p"]) {
      const m = await (await call(`${LIVE}/${tok}/${ch}/${rung}/index.m3u8`)).text();
      expect(m).toMatch(/\n100-[0-9a-f]{8}\.ts\n$/);
    }
  });

  it("answers 503 Retry-After 1 when R2 is unavailable", async () => {
    const ch = newChannel();
    const broken = {
      ...E.SEGMENTS,
      put: async () => {
        throw new Error("R2 down");
      },
    } as unknown as R2Bucket;
    const token = await ingestToken({ ch });
    const body = segmentBody("src", 1);
    const sha = [...new Uint8Array(await crypto.subtle.digest("SHA-256", body))]
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const r = await call(
      `${INGEST}/v1/ingest/${ch}/src/1.ts`,
      {
        method: "PUT",
        headers: {
          Authorization: `Bearer ${token}`,
          "X-Maktaba-Time": String(now()),
          "X-Maktaba-Body-SHA256": sha,
          "X-Maktaba-Duration": "4",
        },
        body,
      },
      { SEGMENTS: broken }
    );
    expect(r.status).toBe(503);
    expect(r.headers.get("Retry-After")).toBe("1");
  });

  it("stops everything with BROADCAST_GLOBAL_OFF = 1", async () => {
    const ch = newChannel();
    const r = await call(
      `${INGEST}/v1/ingest/${ch}/src/1.ts`,
      { method: "PUT" },
      { BROADCAST_GLOBAL_OFF: "1" }
    );
    expect(r.status).toBe(503);
    const tok = await viewerToken({ ch });
    expect(
      (await call(`${LIVE}/${tok}/${ch}/master.m3u8`, undefined, { BROADCAST_GLOBAL_OFF: "1" }))
        .status
    ).toBe(503);
  });

  it("ends a broadcast on {on_air: false}: play is refused 503 off_air until segments resume", async () => {
    const ch = newChannel();
    await putSegment(ch, "src", 1);
    expect((await postState(ch, { on_air: false })).status).toBe(200);
    const tok = await viewerToken({ ch });
    const r = await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`);
    expect(r.status).toBe(503);
    expect(await r.json()).toEqual({ error: "off_air" });
    advanceClock(2000);
    await putSegment(ch, "src", 2);
    expect((await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`)).status).toBe(200);
  });

  it("is off air after 24 h without a segment", async () => {
    const ch = newChannel();
    await putSlate(ch, 0);
    await putSegment(ch, "src", 1);
    advanceClock(24 * 3600_000 + 60_000);
    const tok = await viewerToken({ ch });
    const r = await call(`${LIVE}/${tok}/${ch}/master.m3u8`);
    expect(r.status).toBe(503);
    expect(await r.json()).toEqual({ error: "off_air" });
  });

  it("takes the slate once per index (a retry is 200) and never more than 8", async () => {
    const ch = newChannel();
    expect((await putSlate(ch, 0)).status).toBe(201);
    expect((await putSlate(ch, 0)).status).toBe(200);
    expect((await putSlate(ch, 8)).status).toBe(400);
  });

  it("opens the control socket and says hello", async () => {
    const ch = newChannel();
    await putSegment(ch, "src", 1);
    const r = await call(`${INGEST}/v1/ingest/ws`, {
      headers: {
        Upgrade: "websocket",
        Authorization: `Bearer ${await ingestToken({ ch })}`,
        "X-Maktaba-Time": String(now()),
      },
    });
    expect(r.status).toBe(101);
    const ws = r.webSocket!;
    const got = new Promise<string>((resolve) =>
      ws.addEventListener("message", (e) => resolve(String(e.data)))
    );
    ws.accept();
    expect(JSON.parse(await got)).toMatchObject({ type: "hello", status: "live" });
    ws.close();
  });
});

describe("the cloud's control posts", () => {
  it("applies a signed plan: lowest-only rungs and the plan's kbps; `off` refuses ingest and play", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    const token = await ingestToken({ ch, pub });
    expect((await putSegment(ch, "src", 1, { token })).status).toBe(201);
    await postState(ch, STATE_3, token);
    const plan = await control("plan", {
      pub,
      mode: "full",
      version: 1,
      max_rungs: 2,
      max_top_kbps: 100,
    });
    const r = await call(`${INGEST}/v1/control/plan`, { method: "PUT", body: plan });
    expect(r.status, await r.clone().text()).toBe(200);
    // the third rung is past the plan
    expect((await putSegment(ch, "480p", 1, { token })).status).toBe(403);
    // the master lists only the plan's rungs
    for (const rung of ["720p"]) await putSegment(ch, rung, 1, { token });
    const tok = await viewerToken({ ch, pub });
    const m = await (await call(`${LIVE}/${tok}/${ch}/master.m3u8`)).text();
    expect(m).toContain("src/index.m3u8");
    expect(m).not.toContain("480p/index.m3u8");
    // an older version never replaces a newer one
    const old = await control("plan", { pub, mode: "off", version: 0 });
    expect((await call(`${INGEST}/v1/control/plan`, { method: "PUT", body: old })).status).toBe(
      409
    );
    const off = await control("plan", { pub, mode: "off", version: 2 });
    expect((await call(`${INGEST}/v1/control/plan`, { method: "PUT", body: off })).status).toBe(
      200
    );
    advanceClock(1500);
    expect((await putSegment(ch, "src", 2, { token })).status).toBe(403);
    const p = await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`);
    expect(p.status).toBe(403);
  });

  it("refuses an unsigned or wrongly typed control post", async () => {
    expect(
      (await call(`${INGEST}/v1/control/plan`, { method: "PUT", body: "mkbc1.e30.AAAA" })).status
    ).toBe(401);
    const rev = await control("revocations", { version: 1, pubs: [], channels: [], homes: [] });
    expect((await call(`${INGEST}/v1/control/plan`, { method: "PUT", body: rev })).status).toBe(
      400
    );
  });

  it("installs a newer revocation list: the channel's ingest and playlists are refused, then restored", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    const token = await ingestToken({ ch, pub });
    await putSegment(ch, "src", 1, { token });
    const stub = (E as Env).REVOCATIONS.get((E as Env).REVOCATIONS.idFromName("global"));
    const version = (await stub.current()).version + 1;
    const list = await control("revocations", { version, pubs: [], channels: [ch], homes: [] });
    const r = await call(`${INGEST}/v1/control/revocations`, { method: "PUT", body: list });
    expect(r.status, await r.clone().text()).toBe(200);
    expect(await (await call(`${INGEST}/v1/control/revocations`)).json()).toEqual({ version });
    advanceClock(1500);
    expect(await (await putSegment(ch, "src", 2, { token })).json()).toEqual({ error: "revoked" });
    const tok = await viewerToken({ ch, pub });
    const p = await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`);
    expect(p.status).toBe(403);
    expect(await p.json()).toEqual({ error: "revoked" });
    // an older list is refused; the next one restores the channel
    expect(
      (
        await call(`${INGEST}/v1/control/revocations`, {
          method: "PUT",
          body: await control("revocations", {
            version: version - 1,
            pubs: [],
            channels: [],
            homes: [],
          }),
        })
      ).status
    ).toBe(409);
    const clear = await control("revocations", {
      version: version + 1,
      pubs: [],
      channels: [],
      homes: [],
    });
    expect(
      (await call(`${INGEST}/v1/control/revocations`, { method: "PUT", body: clear })).status
    ).toBe(200);
    advanceClock(1500);
    expect((await putSegment(ch, "src", 2, { token })).status).toBe(201);
  });

  it("revokes every channel of a revoked publisher", async () => {
    const [a, b] = [newChannel(), newChannel()];
    const pub = `pub-${a}`;
    await putSegment(a, "src", 1, { token: await ingestToken({ ch: a, pub }) });
    await putSegment(b, "src", 1, { token: await ingestToken({ ch: b, pub }) });
    const stub = (E as Env).REVOCATIONS.get((E as Env).REVOCATIONS.idFromName("global"));
    const version = (await stub.current()).version + 1;
    await call(`${INGEST}/v1/control/revocations`, {
      method: "PUT",
      body: await control("revocations", { version, pubs: [pub], channels: [], homes: [] }),
    });
    for (const ch of [a, b]) {
      const s = await runInDurableObject(
        (E as Env).CHANNEL.get((E as Env).CHANNEL.idFromName(ch)),
        (o) => o.snapshot()
      );
      expect(s.status).toBe("revoked");
    }
    // leave the list clean for the other tests
    await call(`${INGEST}/v1/control/revocations`, {
      method: "PUT",
      body: await control("revocations", {
        version: version + 1,
        pubs: [],
        channels: [],
        homes: [],
      }),
    });
  });
});
