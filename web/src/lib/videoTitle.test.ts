import { describe, expect, it } from "vitest";
import { cardTitle } from "../bigscreen/ui";
import { stripExt, videoTitle } from "./videoTitle";

// The same cases as the API's Go rule (api/internal/videotitle).
const cases: [string, string][] = [
  ["My Lecture 03.mp4", "My Lecture 03"],
  ["a.b.mkv", "a.b"],
  ["clip.MP4", "clip"],
  ["محاضرة الفقه.mkv", "محاضرة الفقه"],
  ["notes", "notes"],
  [".mp4", ".mp4"],
  ["x.", "x."],
  ["", ""],
  ["Ch. 3 intro", "Ch. 3 intro"],
  ["..mp4", "."],
  [".hidden.mkv", ".hidden"],
];

describe("stripExt", () => {
  it.each(cases)("%j -> %j", (name, want) => {
    expect(stripExt(name)).toBe(want);
  });
});

describe("videoTitle", () => {
  it("keeps a metadata title as it is, even one that looks like a file name", () => {
    expect(videoTitle({ title: "Title.mp4", filename: "f.mp4", id: "v1" })).toBe("Title.mp4");
  });
  it("falls back to the file name without its extension", () => {
    expect(videoTitle({ title: null, filename: "My Lecture 03.mp4", id: "v1" })).toBe(
      "My Lecture 03"
    );
    expect(videoTitle({ title: "", filename: "a.b.mkv", id: "v1" })).toBe("a.b");
    expect(videoTitle({ title: "  ", filename: "a.b.mkv", id: "v1" })).toBe("a.b");
  });
  it("falls back to the id, then nothing", () => {
    expect(videoTitle({ title: null, filename: null, id: "v1" })).toBe("v1");
    expect(videoTitle({})).toBe("");
  });
});

describe("cardTitle", () => {
  it("never shows the extension of a title-less video", () => {
    expect(cardTitle({ title: null, filename: "My Lecture 03.mp4", id: "v1" })).toBe(
      "My Lecture 03"
    );
    expect(cardTitle({ title: "Seerah", filename: "s.mp4", id: "v2" })).toBe("Seerah");
    expect(cardTitle({ id: "v3" })).toBe("v3");
  });
});
