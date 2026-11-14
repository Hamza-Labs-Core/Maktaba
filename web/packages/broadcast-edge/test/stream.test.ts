// T08: the Stream fallback (a fake Stream API): play is a 302 to a signed manifest, and the
// heartbeats are metered as with R2 delivery (50.20's viewer-minutes, the Stream share apart).
import { afterEach, describe, expect, it } from "vitest";
import { runInDurableObject } from "cloudflare:test";
import { advanceClock, resetClock } from "../src/clock";
import type { Env } from "../src/env";
import {
  call,
  control,
  E,
  INGEST,
  ingestToken,
  LIVE,
  newChannel,
  putSegment,
  viewerToken,
} from "./helpers";
import { now } from "../src/clock";

afterEach(() => resetClock());
const env = E as Env;

async function askStream(ch: string, pub: string): Promise<Response> {
  return call(`${INGEST}/v1/ingest/${ch}/stream`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${await ingestToken({ ch, pub })}`,
      "X-Maktaba-Time": String(now()),
    },
  });
}

function streamToken(location: string): { sub: string; kid: string; exp: number } {
  const m = location.match(
    /^https:\/\/customer-testcode\.cloudflarestream\.com\/([^/]+)\/manifest\/video\.m3u8$/
  );
  expect(m, location).not.toBeNull();
  const claims = m![1]!.split(".")[1]!;
  return JSON.parse(atob(claims.replace(/-/g, "+").replace(/_/g, "/"))) as {
    sub: string;
    kid: string;
    exp: number;
  };
}

describe("T08 Stream mode", () => {
  it("redirects a publisher's viewers to a signed Stream manifest, and meters their heartbeats", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    await putSegment(ch, "src", 1, { token: await ingestToken({ ch, pub }) });
    // the publisher's delivery moves to Stream (a plan push, 50.20)
    const plan = await control("plan", { pub, mode: "full", version: 1, delivery: "stream" });
    expect((await call(`${INGEST}/v1/control/plan`, { method: "PUT", body: plan })).status).toBe(
      200
    );
    // the home asks for its live input (one per channel, made once)
    const a = await askStream(ch, pub);
    expect(a.status, await a.clone().text()).toBe(200);
    const input = (await a.json()) as { rtmps: { url: string; stream_key: string } };
    expect(input.rtmps.url).toMatch(/^rtmps:\/\//);
    const again = (await (await askStream(ch, pub)).json()) as typeof input;
    expect(again.rtmps.stream_key).toBe(input.rtmps.stream_key);

    const session = `s-${ch}`;
    const tok = await viewerToken({ ch, pub, session });
    const play = await call(`${LIVE}/${tok}/${ch}/master.m3u8`);
    expect(play.status).toBe(302);
    expect(play.headers.get("Cache-Control")).toBe("no-store");
    const st = streamToken(play.headers.get("Location")!);
    expect(input.rtmps.stream_key).toBe(`key-${st.sub}`);
    expect(st.exp - Math.floor(now() / 1000)).toBeLessThanOrEqual(600);
    // a media playlist on live. redirects too
    expect((await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`)).status).toBe(302);

    // heartbeats: one viewer-minute per 60 s, counted as Stream delivery
    const beat = () => call(`${LIVE}/${tok}/${ch}/beat`, { method: "POST" });
    expect(await (await beat()).json()).toMatchObject({
      counted: true,
      delivery: "stream",
      concurrent: 1,
    });
    expect(await (await beat()).json()).toMatchObject({ counted: false });
    advanceClock(60_000);
    expect(await (await beat()).json()).toMatchObject({ counted: true });
    const pc = env.PUBLISHER.get(env.PUBLISHER.idFromName(pub));
    const usage = await runInDurableObject(pc, (o) => o.pendingUsage());
    const total = usage.reduce(
      (s, w) => ({ full: s.full + w.minutes_full, stream: s.stream + w.minutes_stream }),
      { full: 0, stream: 0 }
    );
    expect(total).toEqual({ full: 2, stream: 2 });
    expect(usage[0]!.key).toMatch(new RegExp(`^${pub}:\\d{4}-\\d{2}-\\d{2}T`));
  });

  it("redirects every channel with BROADCAST_DELIVERY = stream", async () => {
    const ch = newChannel();
    const pub = `pub-${ch}`;
    await askStream(ch, pub);
    const tok = await viewerToken({ ch, pub });
    const r = await call(`${LIVE}/${tok}/${ch}/master.m3u8`, undefined, {
      BROADCAST_DELIVERY: "stream",
    });
    expect(r.status).toBe(302);
    // limits still apply: a session at the limit is refused before any redirect
    const full = await viewerToken({ ch, pub, mode: "wait" });
    expect(
      (await call(`${LIVE}/${full}/${ch}/master.m3u8`, undefined, { BROADCAST_DELIVERY: "stream" }))
        .status
    ).toBe(429);
  });

  it("answers 503 until the channel has a live input", async () => {
    const ch = newChannel();
    const tok = await viewerToken({ ch });
    const r = await call(`${LIVE}/${tok}/${ch}/master.m3u8`, undefined, {
      BROADCAST_DELIVERY: "stream",
    });
    expect(r.status).toBe(503);
    expect(await r.json()).toEqual({ error: "stream_not_ready" });
  });
});
