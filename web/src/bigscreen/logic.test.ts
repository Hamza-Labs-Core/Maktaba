// Epic 46 — big screen's pure pieces: the search keyboard, the player's
// actions (with RTL), Back's target, a series' order and resume point, the
// preferences, and entering / leaving (lib/bigScreen).
import { describe, it, expect, beforeEach } from "vitest";
import { LAYOUTS, applyKey, searchable } from "./screens/keyboard";
import { lessonAfter, playerCommand, seekTo } from "./screens/playerKeys";
import { backTarget, bigPath, depthOf } from "./nav";
import { lessonsInOrder, resumeAt } from "./screens/Series";
import { DEFAULT_PREFS, readPrefs, writePrefs } from "./prefs";
import { enterBigScreen, isBigPath, rememberReturn, returnPath } from "../lib/bigScreen";

describe("the search keyboard", () => {
  it("has ten keys a line in both layouts, every key one character", () => {
    for (const layout of Object.values(LAYOUTS)) {
      for (const line of layout) {
        expect(line).toHaveLength(10);
        for (const k of line) expect(Array.from(k)).toHaveLength(1);
      }
    }
    expect(LAYOUTS.ar.flat()).toContain("ة");
  });

  it("types, spaces once, deletes a character and clears", () => {
    expect(applyKey("", { kind: "char", char: "ع" })).toBe("ع");
    expect(applyKey("", { kind: "space" })).toBe("");
    expect(applyKey("ab", { kind: "space" })).toBe("ab ");
    expect(applyKey("ab ", { kind: "space" })).toBe("ab ");
    expect(applyKey("علم", { kind: "delete" })).toBe("عل");
    expect(applyKey("a😀", { kind: "delete" })).toBe("a");
    expect(applyKey("anything", { kind: "clear" })).toBe("");
  });

  it("searches from two characters", () => {
    expect(searchable(" a ")).toBeNull();
    expect(searchable("ab ")).toBe("ab");
    expect(searchable("علم")).toBe("علم");
  });
});

describe("the player's actions", () => {
  it("the media keys and a pad act whatever is shown", () => {
    for (const shown of [true, false]) {
      expect(playerCommand("playPause", shown, "ltr")).toEqual({ kind: "toggle" });
      expect(playerCommand("seekForward", shown, "ltr")).toEqual({ kind: "seek", by: 30 });
      expect(playerCommand("seekBack", shown, "rtl")).toEqual({ kind: "seek", by: -30 });
      expect(playerCommand("next", shown, "ltr")).toEqual({ kind: "nextLesson" });
      expect(playerCommand("previous", shown, "ltr")).toEqual({ kind: "restart" });
      expect(playerCommand("play", shown, "ltr")).toEqual({ kind: "play" });
      expect(playerCommand("pause", shown, "ltr")).toEqual({ kind: "pause" });
    }
  });

  it("with the controls hidden the arrows seek (mirrored in Arabic) and Enter toggles", () => {
    expect(playerCommand("right", false, "ltr")).toEqual({ kind: "seek", by: 10 });
    expect(playerCommand("left", false, "ltr")).toEqual({ kind: "seek", by: -10 });
    expect(playerCommand("left", false, "rtl")).toEqual({ kind: "seek", by: 10 });
    expect(playerCommand("right", false, "rtl")).toEqual({ kind: "seek", by: -10 });
    expect(playerCommand("select", false, "ltr")).toEqual({ kind: "toggle" });
    expect(playerCommand("up", false, "ltr")).toEqual({ kind: "show" });
    expect(playerCommand("back", false, "ltr")).toBeNull();
  });

  it("with the controls shown the arrows and Enter are the frame's", () => {
    for (const a of ["left", "right", "up", "down", "select", "back"] as const)
      expect(playerCommand(a, true, "ltr")).toBeNull();
  });

  it("keeps a seek inside the video", () => {
    expect(seekTo(5, -10, 600)).toBe(0);
    expect(seekTo(590, 30, 600)).toBe(599);
    expect(seekTo(100, 30, NaN)).toBe(130);
  });

  it("finds the lesson after this one", () => {
    const l = [{ video_id: "a" }, { video_id: "b" }];
    expect(lessonAfter(l, "a")).toEqual({ video_id: "b" });
    expect(lessonAfter(l, "b")).toBeNull();
    expect(lessonAfter(l, "z")).toBeNull();
  });
});

describe("a series", () => {
  const ep = (video_id: string, season: number, episode?: number) => ({
    video_id,
    season,
    episode,
    title: video_id,
    progress_pct: 0,
    watched: false,
  });
  it("orders seasons, then lessons, with specials last", () => {
    const order = lessonsInOrder([
      { season: 0, episodes: [ep("sp", 0, 1)] },
      { season: 2, episodes: [ep("b2", 2, 2), ep("b1", 2, 1)] },
      { season: 1, episodes: [ep("a1", 1, 1), ep("ax", 1)] },
    ]).map((e) => e.video_id);
    expect(order).toEqual(["a1", "ax", "b1", "b2", "sp"]);
  });

  it("resumes a lesson in progress, never a finished or barely started one", () => {
    expect(resumeAt({ progress_pct: 40, watched: false, duration_sec: 1000 })).toBe(400);
    expect(resumeAt({ progress_pct: 0, watched: false, duration_sec: 1000 })).toBe(0);
    expect(resumeAt({ progress_pct: 97, watched: false, duration_sec: 1000 })).toBe(0);
    expect(resumeAt({ progress_pct: 40, watched: true, duration_sec: 1000 })).toBe(0);
    expect(resumeAt({ progress_pct: 40, watched: false })).toBe(0);
  });
});

describe("Back", () => {
  it("goes one entry back, else Home, else asks to leave", () => {
    expect(backTarget("/big/video/1", 2)).toBe("history");
    expect(backTarget("/big/libraries", 0)).toBe("home");
    expect(backTarget("/big", 0)).toBe("exit");
    expect(backTarget("/big/", 0)).toBe("exit");
    expect(depthOf({ bs: 3 })).toBe(3);
    expect(depthOf(null)).toBe(0);
    expect(depthOf({ bs: "x" })).toBe(0);
  });

  it("builds the player's address with its start and series", () => {
    expect(bigPath.watch("v 1", { t: 12.7, series: "S1" })).toBe("/big/watch/v%201?t=12&series=S1");
    expect(bigPath.watch("v1", { t: 0 })).toBe("/big/watch/v1");
  });
});

describe("preferences", () => {
  beforeEach(() => localStorage.clear());
  it("default, round-trip and refuse nonsense", () => {
    expect(readPrefs()).toEqual(DEFAULT_PREFS);
    writePrefs({ autoplayNext: false, subtitlesOn: true, subtitleScale: 1.5 });
    expect(readPrefs()).toEqual({ autoplayNext: false, subtitlesOn: true, subtitleScale: 1.5 });
    localStorage.setItem(
      "mkt:big:prefs",
      JSON.stringify({ autoplayNext: "yes", subtitleScale: 9 })
    );
    expect(readPrefs()).toEqual(DEFAULT_PREFS);
    localStorage.setItem("mkt:big:prefs", "{");
    expect(readPrefs()).toEqual(DEFAULT_PREFS);
  });
});

describe("entering and leaving", () => {
  beforeEach(() => sessionStorage.clear());
  it("remembers the page to return to, never a big-screen one or another site", () => {
    expect(returnPath()).toBe("/");
    enterBigScreen("/library/L1?tab=browse");
    expect(returnPath()).toBe("/library/L1?tab=browse");
    rememberReturn("/big/search");
    expect(returnPath()).toBe("/library/L1?tab=browse");
    sessionStorage.setItem("mkt:big:return", "//evil.example/x");
    expect(returnPath()).toBe("/");
    sessionStorage.setItem("mkt:big:return", "/big");
    expect(returnPath()).toBe("/");
    expect(isBigPath("/big")).toBe(true);
    expect(isBigPath("/big/live/3")).toBe(true);
    expect(isBigPath("/bigger")).toBe(false);
  });
});
