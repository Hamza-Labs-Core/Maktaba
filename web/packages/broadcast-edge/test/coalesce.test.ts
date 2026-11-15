// The per-location read collapse (story 50.21 §4): viewers who all miss a new segment at once
// share one R2 read in their location, with each viewer's token still checked first. The
// nightly fan-out at 1,000 viewers read one object 6 times at one location before this.
import { afterEach, describe, expect, it } from "vitest";
import { cached, fillsInFlight } from "../src/cache";
import { resetClock } from "../src/clock";
import { hex } from "../src/crypto";
import { call, E, LIVE, newChannel, putSegment, segmentBody, viewerToken } from "./helpers";

afterEach(() => resetClock());

/** counting wraps the segments bucket, counting gets by key. */
function counting(): { bucket: R2Bucket; reads: Map<string, number> } {
  const reads = new Map<string, number>();
  const real = E.SEGMENTS;
  const bucket = new Proxy(real, {
    get(target, prop) {
      if (prop === "get")
        return (key: string, o?: R2GetOptions) => {
          reads.set(key, (reads.get(key) ?? 0) + 1);
          return target.get(key, o);
        };
      const v = Reflect.get(target, prop) as unknown;
      return typeof v === "function" ? (v as (...a: unknown[]) => unknown).bind(target) : v;
    },
  });
  return { bucket, reads };
}

describe("simultaneous misses at one location", () => {
  it("read R2 once for N viewers of one new segment", async () => {
    const ch = newChannel();
    expect((await putSegment(ch, "src", 1)).status).toBe(201);
    const body = segmentBody("src", 1);
    const name = `1-${hex(await crypto.subtle.digest("SHA-256", body)).slice(0, 8)}.ts`;
    const N = 50;
    const tokens = await Promise.all(
      Array.from({ length: N }, (_, i) => viewerToken({ ch, session: `c${i}` }))
    );
    const { bucket, reads } = counting();
    // every viewer at once: no first one let through ahead of the rest
    const answers = await Promise.all(
      tokens.map((tok) => call(`${LIVE}/${tok}/${ch}/src/${name}`, undefined, { SEGMENTS: bucket }))
    );
    const bodies = await Promise.all(answers.map((r) => r.arrayBuffer()));
    expect(answers.map((r) => r.status)).toEqual(Array(N).fill(200));
    for (const b of bodies) expect(new Uint8Array(b)).toEqual(body);
    expect([...reads.values()]).toEqual([1]);
    expect(answers.filter((r) => r.headers.get("X-Maktaba-Cache") === "miss")).toHaveLength(1);
    expect(fillsInFlight()).toBe(0);
  });

  it("checks each viewer's token before the shared read", async () => {
    const ch = newChannel();
    expect((await putSegment(ch, "src", 1)).status).toBe(201);
    const name = `1-${hex(await crypto.subtle.digest("SHA-256", segmentBody("src", 1))).slice(0, 8)}.ts`;
    const good = await viewerToken({ ch, session: "ok" });
    const other = await viewerToken({ ch: newChannel(), session: "elsewhere" });
    const { bucket, reads } = counting();
    const [a, b, c] = await Promise.all([
      call(`${LIVE}/${good}/${ch}/src/${name}`, undefined, { SEGMENTS: bucket }),
      call(`${LIVE}/not-a-token/${ch}/src/${name}`, undefined, { SEGMENTS: bucket }),
      call(`${LIVE}/${other}/${ch}/src/${name}`, undefined, { SEGMENTS: bucket }),
    ]);
    expect(a.status).toBe(200);
    expect(b.status).toBeGreaterThanOrEqual(400);
    expect(c.status).toBeGreaterThanOrEqual(400);
    await Promise.all([a.arrayBuffer(), b.arrayBuffer(), c.arrayBuffer()]);
    expect([...reads.values()]).toEqual([1]);
  });

  it("shares only a 200: a failed fill leaves each waiter to fill for itself", async () => {
    const key = `https://coalesce.test/${crypto.randomUUID()}`;
    let fills = 0;
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const first = cached(key, async () => {
      fills++;
      await gate;
      return new Response("busy", { status: 503 });
    });
    const second = cached(key, async () => {
      fills++;
      return new Response("ok", { status: 200, headers: { "Cache-Control": "max-age=60" } });
    });
    release();
    const [a, b] = await Promise.all([first, second]);
    expect(a.res.status).toBe(503);
    expect(b.res.status).toBe(200);
    expect(await b.res.text()).toBe("ok");
    expect(fills).toBe(2);
    // the 200 was stored: the next request is a hit with no fill
    const c = await cached(key, async () => {
      fills++;
      return new Response("again", { status: 200 });
    });
    expect(c.status).toBe("hit");
    expect(await c.res.text()).toBe("ok");
    expect(fills).toBe(2);
  });
});
