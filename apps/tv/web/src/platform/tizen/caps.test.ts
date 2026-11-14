// Story 58.10 — the Samsung device caps reader against shared/playback/caps-vectors.json
// (story 58.02 T07): each Samsung vector's recorded answers must give exactly its
// device_caps. And the probe: what it asks webapis and the decoder, on a shimmed TV.
import vectors from "../../../../../../shared/playback/caps-vectors.json";
import { audioOutput, probeTizen, tizenCaps, type TizenAnswers } from "./caps";

interface Vector {
  id: string;
  profile: string;
  answers: unknown;
  device_caps: unknown;
}

const SAMSUNG = (vectors.vectors as Vector[]).filter((v) => v.profile === "tizen");

describe("the Samsung device caps (58.10, 58.02 vectors)", () => {
  it("has Samsung vectors to check", () => {
    expect(SAMSUNG.length).toBeGreaterThanOrEqual(2);
  });

  for (const v of SAMSUNG) {
    it(`builds ${v.id}'s device_caps from its answers`, () => {
      expect(tizenCaps(v.answers as TizenAnswers)).toEqual(v.device_caps);
    });
  }

  it("never reports Dolby Vision or HDR10+", () => {
    const caps = tizenCaps({
      ...(SAMSUNG[0]!.answers as TizenAnswers),
      hdr: ["HDR10", "HLG", "HDR10+", "DV", "dv-p8"],
    });
    expect(caps.hdr).toEqual(["hdr10", "hlg"]);
  });

  it("drops eac3 passthrough when the output takes only Dolby Digital", () => {
    expect(audioOutput("DOLBY")).toEqual({ passthrough: ["ac3"], max_channels: 6 });
    expect(audioOutput("DOLBY_DIGITAL_PLUS")).toEqual({
      passthrough: ["ac3", "eac3"],
      max_channels: 6,
    });
    expect(audioOutput("PCM")).toEqual({ passthrough: [], max_channels: 2 });
    expect(audioOutput("DTS")).toEqual({ passthrough: [], max_channels: 2 });
    expect(audioOutput(null)).toEqual({ passthrough: [], max_channels: 2 });
  });

  it("reports an FHD panel at 1080", () => {
    const a = SAMSUNG[0]!.answers as TizenAnswers;
    const caps = tizenCaps({
      ...a,
      productinfo: { ...a.productinfo, isUdPanelSupported: false },
    });
    expect(caps.video.every((v) => v.max_height === 1080)).toBe(true);
  });
});

/** A window whose decoder takes the listed MIME types. */
function tv(types: string[]): Window {
  const w = {
    document,
    MediaSource: { isTypeSupported: (t: string) => types.indexOf(t) >= 0 },
  };
  return w as unknown as Window;
}

describe("the Samsung probe (58.10)", () => {
  it("asks the panel, the decoder, HDR and the audio output, as a QN90B answers", () => {
    const w = tv([
      'video/mp4; codecs="avc1.640033"',
      'video/mp4; codecs="hvc1.2.4.L153.B0"',
      'video/mp4; codecs="av01.0.13M.10"',
      'video/webm; codecs="vp09.00.50.08"',
      'audio/mp4; codecs="mp4a.40.2"',
      'audio/mp4; codecs="ac-3"',
      'audio/mp4; codecs="ec-3"',
      "audio/mpeg",
    ]);
    const answers = probeTizen(
      w,
      {
        productinfo: { isUdPanelSupported: () => true, is8KPanelSupported: () => false },
        avinfo: { isHdrTvSupport: () => true },
        audiocontrol: { getOutputMode: () => "DOLBY_DIGITAL_PLUS" },
      },
      2022
    );
    const qn90b = SAMSUNG.find((v) => v.id === "tizen-2022-qn90b")!;
    expect(tizenCaps(answers)).toEqual(qn90b.device_caps);
  });

  it("keeps the defaults when an API is missing or throws", () => {
    const answers = probeTizen(
      tv(['video/mp4; codecs="avc1.640033"', 'audio/mp4; codecs="mp4a.40.2"']),
      {
        productinfo: {
          isUdPanelSupported: () => {
            throw new Error("not allowed");
          },
        },
      },
      0
    );
    expect(answers.productinfo.isUdPanelSupported).toBe(false);
    expect(answers.hdr).toEqual([]);
    expect(answers.audio_output).toEqual({ passthrough: [], max_channels: 2 });
    const caps = tizenCaps(answers);
    expect(caps.video).toEqual([{ codec: "h264", max_height: 1080, max_fps: 60 }]);
    expect(caps.audio).toEqual(["aac"]);
  });
});
