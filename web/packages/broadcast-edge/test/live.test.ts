// T03 (the cache key has no token), T09 (headers), the offline slate and back over HTTP, master
// modes, and T07 (the alarm deletes segment objects older than 10 minutes).
import { afterEach, describe, expect, it } from "vitest";
import { runDurableObjectAlarm, runInDurableObject } from "cloudflare:test";
import { masterCacheKey, mediaCacheKey, segmentCacheKey } from "../src/cache";
import { advanceClock, resetClock } from "../src/clock";
import type { Env } from "../src/env";
import {
  call,
  E,
  LIVE,
  newChannel,
  postState,
  putSegment,
  putSlate,
  STATE_3,
  uris,
  viewerToken,
} from "./helpers";

afterEach(() => resetClock());

const env = E as Env;
const stub = (ch: string) => env.CHANNEL.get(env.CHANNEL.idFromName(ch));

describe("T03 cache key", () => {
  it("is the same for two tokens and never holds the token", async () => {
    const ch = newChannel();
    const [a, b] = [await viewerToken({ ch }), await viewerToken({ ch, session: "other" })];
    expect(a).not.toBe(b);
    for (const key of [
      segmentCacheKey(env, ch, "src", "100-0123abcd.ts"),
      mediaCacheKey(env, ch, "src"),
      masterCacheKey(env, ch, "full"),
    ]) {
      expect(key).not.toContain(a);
      expect(key).not.toContain(b);
      expect(key).not.toContain("mkbv1");
      expect(key.startsWith(`https://live.maktaba.tv/c/${ch}/`)).toBe(true);
    }
  });

  it("serves the second viewer, with another token, from the cache", async () => {
    const ch = newChannel();
    await putSegment(ch, "src", 100);
    const [a, b] = [await viewerToken({ ch }), await viewerToken({ ch })];
    const name = uris(await (await call(`${LIVE}/${a}/${ch}/src/index.m3u8`)).text())[0]!;
    const first = await call(`${LIVE}/${a}/${ch}/src/${name}`);
    expect(first.status).toBe(200);
    expect(first.headers.get("X-Maktaba-Cache")).toBe("miss");
    const second = await call(`${LIVE}/${b}/${ch}/src/${name}`);
    expect(second.headers.get("X-Maktaba-Cache")).toBe("hit");
    expect(new Uint8Array(await second.arrayBuffer())).toEqual(
      new Uint8Array(await first.arrayBuffer())
    );
  });

  it("refuses a segment without a good token before reading anything", async () => {
    const ch = newChannel();
    await putSegment(ch, "src", 100);
    expect((await call(`${LIVE}/mkbv1.e30.AAAA/${ch}/src/100-00000000.ts`)).status).toBe(401);
    const other = await viewerToken({ ch: "another" });
    expect((await call(`${LIVE}/${other}/${ch}/src/100-00000000.ts`)).status).toBe(403);
    expect(
      (await call(`${LIVE}/${await viewerToken({ ch, mode: "wait" })}/${ch}/master.m3u8`)).status
    ).toBe(429);
  });
});

describe("T09 headers", () => {
  it("marks segments immutable and playlists 1 s, with CORS for the watch origin and no cookie", async () => {
    const ch = newChannel();
    await putSegment(ch, "src", 7);
    await postState(ch, STATE_3);
    const tok = await viewerToken({ ch });
    const origin = { headers: { Origin: "https://watch.maktaba.tv" } };
    const media = await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`, origin);
    const master = await call(`${LIVE}/${tok}/${ch}/master.m3u8`, origin);
    const name = uris(await media.clone().text())[0]!;
    const seg = await call(`${LIVE}/${tok}/${ch}/src/${name}`, origin);
    expect(seg.headers.get("Cache-Control")).toBe("public, max-age=86400, immutable");
    expect(seg.headers.get("Content-Type")).toBe("video/mp2t");
    for (const r of [media, master]) {
      expect(r.headers.get("Cache-Control")).toBe("public, max-age=1");
      expect(r.headers.get("Content-Type")).toContain("application/vnd.apple.mpegurl");
    }
    for (const r of [media, master, seg]) {
      expect(r.headers.get("Set-Cookie")).toBeNull();
      expect(r.headers.get("Access-Control-Allow-Origin")).toBe("https://watch.maktaba.tv");
      expect(r.headers.get("X-Content-Type-Options")).toBe("nosniff");
      expect(r.headers.get("Timing-Allow-Origin")).toContain("https://watch.maktaba.tv");
    }
    // an origin not on the list gets no CORS grant
    const other = await call(`${LIVE}/${tok}/${ch}/src/${name}`, {
      headers: { Origin: "https://evil.example" },
    });
    expect(other.headers.get("Access-Control-Allow-Origin")).toBeNull();
    const pre = await call(`${LIVE}/${tok}/${ch}/master.m3u8`, {
      method: "OPTIONS",
      headers: { Origin: "capacitor://localhost" },
    });
    expect(pre.status).toBe(204);
    expect(pre.headers.get("Access-Control-Allow-Origin")).toBe("capacitor://localhost");
  });
});

describe("modes and the offline slate over HTTP", () => {
  it("lists 3 rungs for full and the lowest for lowest", async () => {
    const ch = newChannel();
    for (const rung of ["src", "720p", "480p"]) await putSegment(ch, rung, 1);
    await postState(ch, STATE_3);
    const full = await (
      await call(`${LIVE}/${await viewerToken({ ch })}/${ch}/master.m3u8`)
    ).text();
    expect(uris(full)).toEqual(["src/index.m3u8", "720p/index.m3u8", "480p/index.m3u8"]);
    const lowest = await (
      await call(`${LIVE}/${await viewerToken({ ch, mode: "lowest" })}/${ch}/master.m3u8`)
    ).text();
    expect(uris(lowest)).toEqual(["480p/index.m3u8"]);
  });

  it("continues with the slate 12 s after the home stops, and returns to live when it resumes", async () => {
    const ch = newChannel();
    await putSlate(ch, 0, 2);
    await putSlate(ch, 1, 2);
    for (let s = 1; s <= 3; s++) {
      await putSegment(ch, "src", s);
      await putSegment(ch, "720p", s);
      advanceClock(4000);
    }
    // only src resumes later: 720p stays on the slate while src is live (a rung stops, others go on)
    advanceClock(13_000);
    const tok = await viewerToken({ ch });
    const m = await (await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`)).text();
    expect(uris(m).some((u) => u.startsWith("../slate/"))).toBe(true);
    expect(m).toContain("#EXT-X-DISCONTINUITY\n");
    const slateUri = uris(m).find((u) => u.startsWith("../slate/"))!;
    // the slate segment plays from the edge like any other
    const seg = await call(`${LIVE}/${tok}/${ch}/slate/${slateUri.slice("../slate/".length)}`);
    expect(seg.status).toBe(200);
    advanceClock(1100);
    await putSegment(ch, "src", 4);
    // the cached playlist's 1 s runs on the cache's own (real) clock
    await new Promise((r) => setTimeout(r, 1100));
    const back = await (await call(`${LIVE}/${tok}/${ch}/src/index.m3u8`)).text();
    expect(uris(back).slice(-1)[0]).toMatch(/^4-/);
    const tail = back.split("\n").slice(-5).join("\n");
    expect(tail).toMatch(/#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME/);
    const other = await (await call(`${LIVE}/${tok}/${ch}/720p/index.m3u8`)).text();
    expect(uris(other).slice(-1)[0]).toMatch(/^\.\.\/slate\//);
    expect((await runInDurableObject(stub(ch), (o) => o.snapshot())).status).toBe("live");
  });
});

describe("T07 clean-up alarm", () => {
  it("deletes segment objects 11 minutes old and keeps newer ones and the slate", async () => {
    const ch = newChannel();
    await putSlate(ch, 0);
    for (let s = 1; s <= 3; s++) {
      await putSegment(ch, "src", s);
      advanceClock(400);
    }
    advanceClock(6 * 60_000);
    for (let s = 4; s <= 5; s++) {
      await putSegment(ch, "src", s);
      advanceClock(400);
    }
    // an object nothing tracks (a write whose append never came): the backstop's
    await env.SEGMENTS.put(`test/${ch}/src/999-deadbeef.ts`, "orphan");
    advanceClock(5 * 60_000);
    // segments 1-3 are now 11 minutes old, 4-5 are 5 minutes old
    const before = (await env.SEGMENTS.list({ prefix: `test/${ch}/` })).objects.length;
    expect(before).toBe(7);
    expect(await runDurableObjectAlarm(stub(ch))).toBe(true);
    const keys = (await env.SEGMENTS.list({ prefix: `test/${ch}/` })).objects.map((o) => o.key);
    expect(
      keys
        .filter((k) => k.includes("/src/"))
        .map((k) => k.split("/")[3]!.split("-")[0])
        .sort()
    ).toEqual(["4", "5"]);
    expect(keys.some((k) => k.includes("/slate/"))).toBe(true);
    // a further 10 minutes: the rest of the segments go too; the slate stays while published
    advanceClock(11 * 60_000);
    await runInDurableObject(stub(ch), (o) => o.sweep());
    const left = (await env.SEGMENTS.list({ prefix: `test/${ch}/` })).objects.map((o) => o.key);
    expect(left).toHaveLength(1);
    expect(left[0]).toContain("/slate/");
  });
});
