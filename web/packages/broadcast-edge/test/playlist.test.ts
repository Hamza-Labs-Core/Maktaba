// T02 (ChannelLive's timeline: 40 segments, a discontinuity, a 13 s gap) and T04 (the master
// playlist for modes full / lowest), on the plain functions ChannelLive runs.
import { describe, expect, it } from "vitest";
import {
  advance,
  append,
  classify,
  KEEP,
  masterRungs,
  newRung,
  renderMaster,
  renderMedia,
  rungStatus,
  WINDOW,
  type Pending,
  type RungState,
  type SlateSegment,
} from "../src/playlist";

const T0 = Date.UTC(2026, 10, 13, 12, 0, 0);
const seg = (seq: number, o: Partial<Pending> = {}): Pending => ({
  seq,
  hash8: (0x10000000 + seq).toString(16).slice(0, 8),
  key: `test/c/src/${seq}.ts`,
  dur: 4,
  bytes: 1000,
  disc: false,
  pdt: T0 + seq * 4000,
  ...o,
});
const SLATE: SlateSegment[] = [
  { idx: 0, hash8: "aaaaaaaa", key: "test/c/slate/0-aaaaaaaa.ts", dur: 2, bytes: 100, at: T0 },
  { idx: 1, hash8: "bbbbbbbb", key: "test/c/slate/1-bbbbbbbb.ts", dur: 2, bytes: 100, at: T0 },
];

function feed(rs: RungState, from: number, to: number, start = T0): number {
  let t = start;
  for (let s = from; s <= to; s++) {
    t = start + (s - from) * 4000;
    expect(append(rs, seg(s), t)).toBe("new");
  }
  return t;
}

const tags = (m: string, tag: string) => m.split("\n").filter((l) => l.startsWith(tag));
const lines = (m: string) => m.split("\n").filter((l) => l && !l.startsWith("#"));
const header = (m: string, tag: string) => Number(tags(m, tag)[0]!.split(":")[1]);

describe("T02 ChannelLive timeline", () => {
  it("keeps 30, lists a window of 6, and tags a discontinuity where the home flagged one", () => {
    const rs = newRung();
    let t = feed(rs, 1, 20);
    t += 4000;
    expect(append(rs, seg(21, { disc: true }), t)).toBe("new");
    t = feed(rs, 22, 40, t + 4000);
    expect(rs.entries).toHaveLength(KEEP);
    const m = renderMedia(rs)!;
    expect(lines(m)).toHaveLength(WINDOW);
    expect(lines(m)[WINDOW - 1]).toMatch(/^40-[0-9a-f]{8}\.ts$/);
    expect(header(m, "#EXT-X-MEDIA-SEQUENCE")).toBe(34);
    expect(header(m, "#EXT-X-TARGETDURATION")).toBe(4);
    expect(tags(m, "#EXT-X-VERSION:6")).toHaveLength(1);
    expect(tags(m, "#EXT-X-PROGRAM-DATE-TIME")).toHaveLength(WINDOW);
    // the discontinuity at 21 is out of the window: counted in the discontinuity sequence
    expect(tags(m, "#EXT-X-DISCONTINUITY\n")).toHaveLength(0);
    expect(header(m, "#EXT-X-DISCONTINUITY-SEQUENCE")).toBe(1);
    // seen while it was in the window
    const rs2 = newRung();
    feed(rs2, 1, 20);
    append(rs2, seg(21, { disc: true }), T0 + 84_000);
    expect(renderMedia(rs2)!).toContain("#EXT-X-DISCONTINUITY\n");
    expect(rungStatus(rs, t)).toBe("live");
  });

  it("goes to the slate after a 13 s gap (after a discontinuity) and back to live after another", () => {
    const rs = newRung();
    const last = feed(rs, 1, 10);
    // 12 s is still live (3 target durations)
    expect(advance(rs, last + 12_000, SLATE)).toEqual([]);
    expect(rungStatus(rs, last + 12_000)).toBe("live");
    // 13 s: offline, the slate starts at 12 s with a discontinuity
    expect(advance(rs, last + 13_000, SLATE)).toEqual(["stalled"]);
    expect(rungStatus(rs, last + 13_000)).toBe("offline_slate");
    let m = renderMedia(rs)!;
    expect(lines(m).slice(-1)[0]).toBe("../slate/0-aaaaaaaa.ts");
    expect(m).toMatch(
      /#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:[^\n]+\n#EXTINF:2.000,\n\.\.\/slate\/0-aaaaaaaa\.ts/
    );
    // the slate loops with edge time: 10 s later, 5 more entries, one discontinuity
    advance(rs, last + 23_000, SLATE);
    m = renderMedia(rs)!;
    expect(lines(m).filter((l) => l.startsWith("../slate/"))).toHaveLength(6);
    expect(lines(m)).toContain("../slate/1-bbbbbbbb.ts");
    // the media sequence only goes up
    const seqBefore = header(m, "#EXT-X-MEDIA-SEQUENCE");
    // the home is back
    expect(append(rs, seg(11), last + 24_000)).toBe("new");
    m = renderMedia(rs)!;
    expect(lines(m).slice(-1)[0]).toMatch(/^11-/);
    expect(m.split("\n").slice(-5).join("\n")).toMatch(
      /#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:[^\n]+\n#EXTINF:4.000,\n11-/
    );
    expect(header(m, "#EXT-X-MEDIA-SEQUENCE")).toBeGreaterThan(seqBefore);
    expect(rungStatus(rs, last + 24_000)).toBe("live");
  });

  it("without a slate a silent rung stays as it was (no entries invented)", () => {
    const rs = newRung();
    const last = feed(rs, 1, 3);
    advance(rs, last + 60_000, []);
    expect(rs.entries).toHaveLength(3);
    expect(rungStatus(rs, last + 60_000)).toBe("offline_slate");
    expect(rungStatus(rs, last + 25 * 3600_000)).toBe("off_air");
  });

  it("inserts an early segment in order once the gap fills, and drops one older than its slot", () => {
    const rs = newRung();
    feed(rs, 1, 5);
    const t = T0 + 20_000;
    // 7 before 6: 7 waits
    expect(append(rs, seg(7), t)).toBe("new");
    expect(lines(renderMedia(rs)!).slice(-1)[0]).toMatch(/^5-/);
    expect(append(rs, seg(6), t + 100)).toBe("new");
    expect(
      lines(renderMedia(rs)!)
        .slice(-2)
        .map((l) => l.split("-")[0])
    ).toEqual(["6", "7"]);
    expect(renderMedia(rs)!).not.toContain("#EXT-X-DISCONTINUITY\n");
    // a retry of 3 (published) with the same body is a duplicate; 2 with another body conflicts
    expect(classify(rs, 3, seg(3).hash8)).toBe("duplicate");
    expect(classify(rs, 2, "ffffffff")).toBe("conflict");
    // a gap never filled: skipped after one target duration, the next carries a discontinuity
    expect(append(rs, seg(9), t + 200)).toBe("new");
    expect(advance(rs, t + 200 + 4100, [])).toEqual(["skipped"]);
    expect(lines(renderMedia(rs)!).slice(-1)[0]).toMatch(/^9-/);
    expect(renderMedia(rs)!).toContain("#EXT-X-DISCONTINUITY\n");
    // 8 arrives late, after its slot was skipped: stale (200, nothing written)
    expect(classify(rs, 8, seg(8).hash8)).toBe("stale");
    // and anything older than the window is stale too (while kept, another body conflicts)
    expect(classify(rs, 1, "00000000")).toBe("conflict");
    for (let s = 10; s <= 45; s++) append(rs, seg(s), t + s * 4000);
    expect(classify(rs, 1, "00000000")).toBe("stale");
  });
});

describe("T04 master playlist", () => {
  const info = [
    { name: "src", bandwidth: 6_500_000, resolution: "1920x1080", codecs: "avc1.640028,mp4a.40.2" },
    { name: "720p", bandwidth: 3_000_000, resolution: "1280x720" },
    { name: "480p", bandwidth: 1_200_000, resolution: "854x480" },
  ];
  it("lists 3 rungs for full and 1 (the lowest) for lowest", () => {
    const full = renderMaster(masterRungs(info, "full"));
    expect(tags(full, "#EXT-X-STREAM-INF")).toHaveLength(3);
    expect(full).toContain(
      '#EXT-X-STREAM-INF:BANDWIDTH=6500000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2"\nsrc/index.m3u8'
    );
    const lowest = renderMaster(masterRungs(info, "lowest"));
    expect(tags(lowest, "#EXT-X-STREAM-INF")).toHaveLength(1);
    expect(lowest).toContain("BANDWIDTH=1200000,RESOLUTION=854x480\n480p/index.m3u8");
  });
  it("leaves out rungs the plan no longer allows", () => {
    expect(masterRungs(info, "full", ["src", "720p"]).map((r) => r.name)).toEqual(["src", "720p"]);
    expect(masterRungs(info, "lowest", ["src", "720p"]).map((r) => r.name)).toEqual(["720p"]);
  });
});
