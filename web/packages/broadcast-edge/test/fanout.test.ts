// T06: one home, 3 rungs, 10 minutes of segments, 200 viewers in one location. The home's PUTs
// are segments × rungs whatever the viewers; R2 reads are one per segment per rung (the
// location's misses); every other viewer is a cache hit.
//
// Miniflare has one location and no cache lock, so the first viewer of each segment is let
// through before the rest, as Cloudflare's cache lock holds simultaneous misses on one key.
import { afterEach, describe, expect, it } from "vitest";
import { advanceClock, resetClock } from "../src/clock";
import { hex } from "../src/crypto";
import {
  call,
  ingestToken,
  LIVE,
  newChannel,
  putSegment,
  RUNGS,
  segmentBody,
  viewerToken,
} from "./helpers";

afterEach(() => resetClock());

const SEGMENTS = 150; // 10 minutes at 4 s
const VIEWERS = 200;

describe("T06 fan-out", () => {
  it("fetches from the home once per segment and rung, and reads R2 once per segment and rung", async () => {
    const ch = newChannel();
    const token = await ingestToken({ ch });
    // viewer tokens live 10 minutes: watch.maktaba.tv mints new ones as players ask (50.26)
    const mint = () =>
      Promise.all(Array.from({ length: VIEWERS }, (_, i) => viewerToken({ ch, session: `v${i}` })));
    let tokens = await mint();
    let puts = 0;
    let misses = 0;
    let hits = 0;
    let playlistMisses = 0;
    for (let s = 1; s <= SEGMENTS; s++) {
      if (s % 100 === 0) tokens = await mint();
      for (const rung of RUNGS) {
        const r = await putSegment(ch, rung, s, { token, dur: 4 });
        expect(r.status).toBe(201);
        puts++;
      }
      advanceClock(4000);
      // each viewer watches one rung; viewers of a rung read its playlist and the new segment
      for (const [ri, rung] of RUNGS.entries()) {
        const mine = tokens.filter((_, i) => i % RUNGS.length === ri);
        const pl = await call(`${LIVE}/${mine[0]}/${ch}/${rung}/index.m3u8`);
        if (pl.headers.get("X-Maktaba-Cache") === "miss") playlistMisses++;
        await pl.arrayBuffer();
        // the new segment's name (the playlist names it within a second; its cached copy, read in
        // the same real second, may still name the one before)
        const name = `${s}-${hex(await crypto.subtle.digest("SHA-256", segmentBody(rung, s))).slice(0, 8)}.ts`;
        const fetchOne = async (tok: string) => {
          const r = await call(`${LIVE}/${tok}/${ch}/${rung}/${name}`);
          expect(r.status).toBe(200);
          await r.arrayBuffer();
          if (r.headers.get("X-Maktaba-Cache") === "miss") misses++;
          else hits++;
        };
        await fetchOne(mine[0]!);
        await Promise.all(mine.slice(1).map(fetchOne));
      }
    }
    expect(puts).toBe(SEGMENTS * RUNGS.length);
    expect(misses).toBeLessThanOrEqual(SEGMENTS * RUNGS.length);
    expect(misses).toBe(SEGMENTS * RUNGS.length);
    expect(hits).toBe(SEGMENTS * (VIEWERS - RUNGS.length));
    expect(playlistMisses).toBeGreaterThan(0);
  });
});
