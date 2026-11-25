import { describe, expect, it } from "vitest";
import { cueLines, raiseCue, raiseCues } from "./cues";

const WIDE = 16 / 9;

function cue(text: string, over: Record<string, unknown> = {}) {
  return {
    text,
    line: "auto" as number | "auto",
    snapToLines: true,
    vertical: "" as const,
    size: 100,
    region: null,
    ...over,
  };
}

describe("cueLines", () => {
  it("counts the cue's lines, and those a long one wraps to", () => {
    expect(cueLines("one", WIDE, 1)).toBe(1);
    expect(cueLines("one\ntwo", WIDE, 1)).toBe(2);
    expect(cueLines("<v Reader><i>one</i>", WIDE, 1)).toBe(1);
    expect(cueLines("word ".repeat(40), WIDE, 1)).toBe(3);
    // a narrow cue, capitals and whole-em letters wrap sooner
    expect(cueLines("one two three four", WIDE, 1, 20)).toBe(2);
    expect(cueLines("WORDS ".repeat(10), WIDE, 1)).toBe(2);
    expect(cueLines("字".repeat(40), WIDE, 1)).toBe(2);
    // a larger subtitle wraps sooner
    expect(cueLines("word ".repeat(10), WIDE, 1)).toBe(1);
    expect(cueLines("word ".repeat(10), WIDE, 2)).toBe(2);
  });
});

describe("raiseCue", () => {
  it("moves a cue left to the player one line up, whatever its lines", () => {
    const one = cue("تأكّد الهبوط.");
    expect(raiseCue(one, WIDE, 1)).toBe(true);
    expect(one.line).toBe(-2);
    const two = cue("one\ntwo");
    raiseCue(two, WIDE, 1);
    expect(two.line).toBe(-3);
  });

  it("leaves a cue that says where it goes", () => {
    for (const placed of [
      { line: 0 },
      { line: 0, snapToLines: false },
      { line: -1 },
      { snapToLines: false },
      { vertical: "rl" },
      { region: {} },
    ]) {
      const c = cue("top", placed);
      const before = c.line;
      expect(raiseCue(c as never, WIDE, 1)).toBe(false);
      expect(c.line).toBe(before);
    }
  });
});

describe("raiseCues", () => {
  function video(cues: ReturnType<typeof cue>[], mode = "showing") {
    const el = document.createElement("video");
    const track = { mode, cues };
    const list = Object.assign(new EventTarget(), { 0: track, length: 1 });
    Object.defineProperty(el, "textTracks", { value: list });
    return { el, track, list };
  }

  it("moves the cues there and those that come while it plays, and puts them back", () => {
    const a = cue("a");
    const top = cue("top", { line: 0 });
    const { el, track } = video([a, top]);
    const stop = raiseCues(el, 1);
    expect([a.line, top.line]).toEqual([-2, 0]);
    const later = cue("b\nc");
    track.cues.push(later);
    el.dispatchEvent(new Event("timeupdate"));
    expect(later.line).toBe(-3);
    stop();
    expect([a.line, top.line, later.line]).toEqual(["auto", 0, "auto"]);
    const after = cue("d");
    track.cues.push(after);
    el.dispatchEvent(new Event("timeupdate"));
    expect(after.line).toBe("auto");
  });

  it("waits for a track to be turned on", () => {
    const a = cue("a");
    const { el, track, list } = video([a], "disabled");
    raiseCues(el, 1);
    expect(a.line).toBe("auto");
    track.mode = "showing";
    list.dispatchEvent(new Event("change"));
    expect(a.line).toBe(-2);
  });
});
