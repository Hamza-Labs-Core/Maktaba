// T02/T03's web twin (story 58.06): every case of shared/cast/load-vectors.json, which the
// Android and Apple senders run too.
import { describe, expect, it } from "vitest";
import { castData, tvReachable } from "./castData";

// the shared file is outside src/: read it at run time, as redact.test.ts reads its cases
const FS = "node:fs";
const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
  readFileSync: (path: string, encoding: "utf8") => string;
};

interface VectorCase {
  name: string;
  sender_base: string;
  discovery_urls: string[];
  api_level: number;
  lang: string;
  video_id?: string;
  queue_id?: string;
  queue_pos?: number;
  start_sec: number;
  grant: string;
  expect: Record<string, unknown> | null;
}

const vectors = JSON.parse(readFileSync("../shared/cast/load-vectors.json", "utf8")) as {
  cases: VectorCase[];
};

describe("castData (shared/cast/load-vectors.json)", () => {
  it("has cases", () => expect(vectors.cases.length).toBeGreaterThan(3));
  for (const c of vectors.cases) {
    it(c.name, () => {
      const got = castData({
        senderBase: c.sender_base,
        discoveryUrls: c.discovery_urls,
        grant: c.grant,
        videoId: c.video_id,
        queueId: c.queue_id,
        queuePos: c.queue_pos,
        startSec: c.start_sec,
        lang: c.lang,
        apiLevel: c.api_level,
      });
      expect(got).toEqual(c.expect);
    });
  }
});

describe("tvReachable", () => {
  it.each([
    ["https://h1.relay.maktaba.tv/", "https://h1.relay.maktaba.tv"],
    ["https://home.example.org:8444/base/", "https://home.example.org:8444/base"],
    ["http://home.example.org", null],
    ["https://maktaba.local.", null],
    ["https://app.localhost", null],
    ["https://10.0.0.2:8444", null],
    ["https://[::1]:8444", null],
    ["not a url", null],
  ])("%s → %s", (raw, want) => expect(tvReachable(raw)).toBe(want));

  it("never carries a token or header: the data has only the contract's keys", () => {
    const d = castData({
      senderBase: "https://h1.relay.maktaba.tv",
      discoveryUrls: [],
      grant: "cast_x",
      videoId: "v",
      startSec: 3,
      lang: "en",
      apiLevel: 1,
    });
    expect(Object.keys(d ?? {}).sort()).toEqual(
      ["api_level", "grant", "home", "lang", "start_sec", "urls", "video_id"].sort()
    );
  });
});
