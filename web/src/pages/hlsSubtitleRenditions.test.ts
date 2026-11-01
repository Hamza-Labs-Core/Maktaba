// Story 48.4: the streaming service's master lists a video session's
// subtitles as HLS renditions (EXT-X-MEDIA TYPE=SUBTITLES). The real hls.js
// (not the mock VideoPlayer's tests use) parses the streaming service's golden
// masters: the one with subtitles yields a subtitle track per language, off by
// default, and the variants still play; the one without has none.
//
// The masters below are streaming/internal/handlers/testdata/*.m3u8, copied
// (Vite serves nothing outside web/): the Go test
// TestSubtitleRenditions_WebTestCarriesTheGoldens fails when they differ.
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import Hls from "hls.js";

// master_subtitles_signed.m3u8
const withSubtitles = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="العربية",LANGUAGE="ar",DEFAULT=NO,AUTOSELECT=YES,URI="subs-ar/index.m3u8?sig=tok.one"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=YES,URI="subs-en/index.m3u8?sig=tok.one"
#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.64002a,mp4a.40.2",SUBTITLES="subs"
v0/index.m3u8?sig=tok.one
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1280x720,CODECS="avc1.64002a,mp4a.40.2",SUBTITLES="subs"
v1/index.m3u8?sig=tok.one
#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=854x480,CODECS="avc1.64002a,mp4a.40.2",SUBTITLES="subs"
v2/index.m3u8?sig=tok.one
`;

// master_no_subtitles.m3u8
const withoutSubtitles = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-STREAM-INF:BANDWIDTH=6000000,RESOLUTION=1920x1080,CODECS="avc1.64002a,mp4a.40.2"
v0/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1280x720,CODECS="avc1.64002a,mp4a.40.2"
v1/index.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1200000,RESOLUTION=854x480,CODECS="avc1.64002a,mp4a.40.2"
v2/index.m3u8
`;

const SID = "11111111-2222-4333-8444-555555555555";
const MASTER = `http://home.test/stream/${SID}/manifest.m3u8?sig=tok.one`;

interface Parsed {
  levels: { url: string[] }[];
  subtitleTracks: { name: string; lang?: string; default: boolean; url: string }[];
}

/** Has the real hls.js load the master (served as `body`) and returns what it parsed. */
function parse(body: string): Promise<Parsed> {
  class GoldenLoader {
    stats = {
      aborted: false,
      loaded: 0,
      retry: 0,
      total: 0,
      chunkCount: 0,
      bwEstimate: 0,
      loading: { start: 0, first: 0, end: 0 },
      parsing: { start: 0, end: 0 },
      buffering: { start: 0, first: 0, end: 0 },
    };
    context: unknown;
    load(
      context: { url: string },
      _config: unknown,
      callbacks: {
        onSuccess: (r: { url: string; data: string }, s: unknown, c: unknown) => void;
      }
    ) {
      this.context = context;
      const now = performance.now();
      this.stats.loading = { start: now, first: now, end: now };
      this.stats.loaded = this.stats.total = body.length;
      queueMicrotask(() =>
        callbacks.onSuccess({ url: context.url, data: body }, this.stats, context)
      );
    }
    abort() {}
    destroy() {}
  }
  const hls = new Hls({ loader: GoldenLoader as never, autoStartLoad: false });
  return new Promise((resolve, reject) => {
    hls.on(Hls.Events.MANIFEST_PARSED, (_e, data) => {
      // copied: destroy() empties hls.js's own track lists
      const d = data as unknown as Parsed;
      resolve({
        levels: d.levels.map((l) => ({ url: [...l.url] })),
        subtitleTracks: d.subtitleTracks.map((t) => ({ ...t })),
      });
      hls.destroy();
    });
    hls.on(Hls.Events.ERROR, (_e, data) => {
      if (data.fatal) reject(new Error(data.details));
    });
    hls.loadSource(MASTER);
  });
}

describe("hls.js and the master's subtitle renditions (story 48.4)", () => {
  // jsdom has no Media Source Extensions: hls.js asks it which codecs play
  const g = globalThis as { MediaSource?: unknown };
  const had = g.MediaSource;
  beforeAll(() => {
    g.MediaSource = { isTypeSupported: () => true };
  });
  afterAll(() => {
    g.MediaSource = had;
  });

  it("lists each subtitle as a track, none on by default, signed, beside the variants", async () => {
    const parsed = await parse(withSubtitles);
    expect(parsed.levels).toHaveLength(3);
    expect(parsed.levels.map((l) => l.url[0]).sort()).toEqual(
      ["v0", "v1", "v2"].map((v) => `http://home.test/stream/${SID}/${v}/index.m3u8?sig=tok.one`)
    );
    expect(
      parsed.subtitleTracks.map((t) => ({
        name: t.name,
        lang: t.lang,
        default: t.default,
        url: t.url,
      }))
    ).toEqual([
      {
        name: "العربية",
        lang: "ar",
        default: false,
        url: `http://home.test/stream/${SID}/subs-ar/index.m3u8?sig=tok.one`,
      },
      {
        name: "English",
        lang: "en",
        default: false,
        url: `http://home.test/stream/${SID}/subs-en/index.m3u8?sig=tok.one`,
      },
    ]);
  });

  it("parses a master without subtitles as before: the variants, no subtitle track", async () => {
    const parsed = await parse(withoutSubtitles);
    expect(parsed.levels).toHaveLength(3);
    expect(parsed.subtitleTracks).toEqual([]);
  });
});
