// Epic 58 (58.10 T02, 58.11 T01) — the shared subtitle layer: the WebVTT reader, the cue
// picker, the direction (Arabic right to left, from the language or the text) and the sizes.
import { cueText, cuesAt, loadVtt, parseTime, parseVtt } from "./index";
import { BASE_FONT_PX, SUBTITLE_SCALES, SubtitleLayer, textDir, validScale } from "./layer";

const VTT = `\uFEFFWEBVTT - Maktaba
X-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000

NOTE a comment
that spans lines

STYLE
::cue { color: red }

1
00:00:01.000 --> 00:00:03.500 align:center line:90%
<v Huda>مرحبًا بكم</v>
في <i>مكتبة</i>

00:04.000 --> 00:05.000
Fish &amp; chips &lt;3

01:00:00.250 --> 01:00:01.000
<c.yellow>late</c> <00:00:01.500>cue

00:00:09.000 --> 00:00:08.000
backwards, dropped
`;

describe("parseVtt", () => {
  it("reads the cues and skips NOTE, STYLE, the header and bad timings", () => {
    const cues = parseVtt(VTT);
    expect(cues).toEqual([
      { start: 1, end: 3.5, text: "مرحبًا بكم\nفي مكتبة" },
      { start: 4, end: 5, text: "Fish & chips <3" },
      { start: 3600.25, end: 3601, text: "late cue" },
    ]);
  });
  it("is empty for a file that is not WebVTT", () => {
    expect(parseVtt("1\n00:00:01,000 --> 00:00:02,000\nSRT\n")).toEqual([]);
    expect(parseVtt("")).toEqual([]);
  });
  it("reads CRLF files", () => {
    expect(parseVtt("WEBVTT\r\n\r\n00:01.000 --> 00:02.000\r\nhi\r\n")).toEqual([
      { start: 1, end: 2, text: "hi" },
    ]);
  });
  it("reads times with and without hours", () => {
    expect(parseTime("00:00:01.5")).toBe(1.5);
    expect(parseTime("02:03.040")).toBeCloseTo(123.04);
    expect(parseTime("1:00:00.000")).toBe(3600);
    expect(parseTime("nope")).toBeNaN();
  });
  it("strips tags so no markup from a file reaches the page", () => {
    expect(cueText('<b onclick="x()">bold</b><img src=x onerror=alert(1)>')).toBe("bold");
  });
  it("picks the cues showing at a time", () => {
    const cues = parseVtt(VTT);
    expect(cuesAt(cues, 0.5)).toEqual([]);
    expect(cuesAt(cues, 1).map((c) => c.start)).toEqual([1]);
    expect(cuesAt(cues, 3.5)).toEqual([]);
  });
});

describe("loadVtt", () => {
  it("reads a fetched file and reports failures", async () => {
    const ok = await loadVtt("https://h/s.vtt?sig=x", async () => new Response(VTT));
    expect(ok).toHaveLength(3);
    const errors: string[] = [];
    expect(
      await loadVtt(
        "u",
        async () => new Response("", { status: 403 }),
        (e) => errors.push(e)
      )
    ).toEqual([]);
    expect(
      await loadVtt(
        "u",
        async () => {
          throw new Error("offline");
        },
        (e) => errors.push(e)
      )
    ).toEqual([]);
    expect(errors).toEqual(["HTTP 403", "offline"]);
  });
});

describe("textDir", () => {
  it("is rtl for Arabic and the other RTL languages", () => {
    expect(textDir("anything", "ar")).toBe("rtl");
    expect(textDir("anything", "ar-SA")).toBe("rtl");
    expect(textDir("anything", "he")).toBe("rtl");
    expect(textDir("مرحبا", "en")).toBe("ltr");
  });
  it("reads the first strong letter when there is no language (AVPlay's text)", () => {
    expect(textDir("- 12 مرحبا Maktaba")).toBe("rtl");
    expect(textDir("12: Hello مرحبا")).toBe("ltr");
    expect(textDir("123 ...")).toBe("ltr");
  });
});

describe("SubtitleLayer", () => {
  let host: HTMLElement;
  beforeEach(() => {
    document.body.replaceChildren();
    host = document.createElement("div");
    document.body.appendChild(host);
  });

  it("draws the cues at a time, Arabic right to left, as text only", () => {
    const layer = new SubtitleLayer(host);
    expect(layer.element.hidden).toBe(true);
    layer.setCues(parseVtt(VTT), "ar");
    layer.render(2);
    const cue = layer.element.querySelector(".ml-cue") as HTMLElement;
    expect(layer.element.hidden).toBe(false);
    expect(cue.dir).toBe("rtl");
    expect(cue.lang).toBe("ar");
    expect(cue.textContent).toBe("مرحبًا بكم\nفي مكتبة");
    expect(layer.element.querySelector("i")).toBeNull();
    layer.render(3.6);
    expect(layer.element.hidden).toBe(true);
    expect(layer.text()).toBe("");
  });

  it("shows AVPlay's text, its direction from the text", () => {
    const layer = new SubtitleLayer(host);
    layer.showText("<b>أهلًا</b>");
    const cue = layer.element.querySelector(".ml-cue") as HTMLElement;
    expect(cue.dir).toBe("rtl");
    // its tags removed, and set as text, never markup
    expect(cue.textContent).toBe("أهلًا");
    expect(layer.element.querySelector("b")).toBeNull();
    layer.showText("Hello", "en");
    expect((layer.element.querySelector(".ml-cue") as HTMLElement).dir).toBe("ltr");
    layer.showText(null);
    expect(layer.element.hidden).toBe(true);
  });

  it("sizes the text by the person's choice, 1 for anything else", () => {
    const layer = new SubtitleLayer(host);
    expect(layer.element.style.fontSize).toBe(`${BASE_FONT_PX}px`);
    for (const s of SUBTITLE_SCALES) {
      layer.setScale(s);
      expect(layer.element.style.fontSize).toBe(`${Math.round(BASE_FONT_PX * s)}px`);
    }
    layer.setScale(7);
    expect(layer.currentScale()).toBe(1);
    expect(validScale("2")).toBe(1);
  });

  it("does not redraw the same text on every tick", () => {
    const layer = new SubtitleLayer(host);
    layer.setCues(parseVtt(VTT), "ar");
    layer.render(1.5);
    const first = layer.element.firstChild;
    layer.render(2.5);
    expect(layer.element.firstChild).toBe(first);
    layer.clear();
    expect(layer.element.childNodes).toHaveLength(0);
  });

  it("matches /tv/'s sizes", async () => {
    const { SUBTITLE_SCALES: tv } = await import("../../../../../web/src/bigscreen/prefs");
    expect([...SUBTITLE_SCALES]).toEqual([...tv]);
  });
});
