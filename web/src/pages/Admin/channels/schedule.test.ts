// The grid model: slot layout per day, overlaps, split / add / delete,
// daily ↔ weekly, live broadcasts on the grid, validation.
import { describe, expect, it } from "vitest";
import {
  endMinutes,
  fromMinutes,
  isWeekly,
  layoutLive,
  layoutSlots,
  liveProblems,
  newSlot,
  slotDays,
  slotProblems,
  splitSlot,
  toDaily,
  toMinutes,
  toWeekly,
  type Slot,
} from "./schedule";
import { renumber, moveItem } from "./ChannelList";
import { fillerSpec, shares } from "./FillerSection";
import { collapseFiller } from "./PreviewSection";
import type { Channel, Program } from "../../../lib/channels";

const lane = (start: string, end: string, extra: Partial<Slot> = {}): Slot => ({
  start,
  end,
  source: { type: "lane", lane: "kids.weekly", shows: ["Prophets"] },
  ...extra,
});

describe("time helpers", () => {
  it("parses and formats HH:MM", () => {
    expect(toMinutes("06:30")).toBe(390);
    expect(toMinutes("24:00")).toBe(1440);
    expect(toMinutes("24:30")).toBeNull();
    expect(toMinutes("7")).toBeNull();
    expect(fromMinutes(1440)).toBe("24:00");
    expect(fromMinutes(75)).toBe("01:15");
    // an end of 00:00 is the end of the day
    expect(endMinutes("00:00")).toBe(1440);
  });
  it("reads days; absent means every day", () => {
    expect(slotDays({})).toHaveLength(7);
    expect(slotDays({ days: ["Friday", "mon"] })).toEqual(["mon", "fri"]);
    expect(slotDays({ days: ["nope"] })).toHaveLength(7);
  });
});

describe("layoutSlots", () => {
  it("puts a daily slot on all seven days and a weekly one on its days", () => {
    const blocks = layoutSlots([lane("06:00", "09:00"), lane("12:00", "13:00", { days: ["fri"] })]);
    expect(blocks.filter((b) => b.index === 0)).toHaveLength(7);
    const fri = blocks.filter((b) => b.index === 1);
    expect(fri.map((b) => [b.day, b.start, b.end])).toEqual([["fri", 720, 780]]);
    expect(blocks[0].cls).toBe("lane");
    expect(blocks[0].label).toBe("Prophets");
  });
  it("marks overlaps on the same day only", () => {
    const blocks = layoutSlots([
      lane("06:00", "09:00", { days: ["mon"] }),
      lane("08:00", "10:00", { days: ["mon", "tue"] }),
    ]);
    expect(blocks.filter((b) => b.day === "mon").every((b) => b.clash)).toBe(true);
    expect(blocks.find((b) => b.day === "tue")?.clash).toBe(false);
  });
  it("skips a slot whose end is not after its start", () => {
    expect(layoutSlots([lane("09:00", "08:00")])).toEqual([]);
  });
  it("colours a flex or filler slot as filler", () => {
    const [b] = layoutSlots([{ start: "00:00", end: "24:00", kind: "flex", days: ["sun"] }]);
    expect(b.cls).toBe("filler");
    expect(b.end).toBe(1440);
  });
});

describe("editing slots", () => {
  it("splits a slot on the half hour, keeping its source", () => {
    const out = splitSlot([lane("06:00", "09:00")], 0);
    expect(out.map((s) => [s.start, s.end])).toEqual([
      ["06:00", "07:30"],
      ["07:30", "09:00"],
    ]);
    expect(out[1].source?.lane).toBe("kids.weekly");
    out[1].source!.lane = "changed";
    expect(out[0].source?.lane).toBe("kids.weekly"); // a copy, not shared
  });
  it("adds a one-hour filler slot snapped to the half hour", () => {
    expect(newSlot(9 * 60 + 17)).toEqual({
      start: "09:00",
      end: "10:00",
      source: { type: "filler" },
    });
    expect(newSlot(23 * 60 + 50).end).toBe("24:00");
    expect(newSlot(60, { day: "fri" }).days).toEqual(["fri"]);
  });
  it("switches between daily and weekly", () => {
    const weekly = toWeekly([lane("06:00", "07:00")]);
    expect(weekly[0].days).toHaveLength(7);
    expect(isWeekly(weekly)).toBe(false); // all seven days is still a daily grid
    weekly[0].days = ["fri"];
    expect(isWeekly(weekly)).toBe(true);
    expect(toDaily(weekly)[0].days).toBeUndefined();
  });
  it("validates times, sources and overlaps", () => {
    const slots = [
      lane("06:00", "09:00"),
      { start: "08:00", end: "07:00", source: { type: "series" } },
    ];
    expect(slotProblems(slots[1], slots, 1)).toEqual(
      expect.arrayContaining(["channels.slot.err.order", "channels.slot.err.source"])
    );
    const both = [lane("06:00", "09:00"), lane("08:00", "10:00")];
    expect(slotProblems(both[0], both, 0)).toEqual(["channels.slot.err.overlap"]);
    expect(slotProblems(lane("06:00", "24:00"), [lane("06:00", "24:00")], 0)).toEqual([]);
  });
});

describe("live broadcasts on the grid", () => {
  it("lays out a row's airings for its channel only", () => {
    const rows = [
      {
        channel: 105,
        url: "https://x/live",
        title: "News",
        start: "20:00",
        end: "22:00",
        every_minutes: 60,
        minutes: 10,
        days: ["fri"],
      },
      { channel: 106, url: "https://y", start: "00:00" },
    ];
    const blocks = layoutLive(rows, 105);
    expect(blocks.map((b) => [b.day, b.start, b.end])).toEqual([
      ["fri", 1200, 1210],
      ["fri", 1260, 1270],
    ]);
    expect(blocks[0].label).toBe("News");
  });
  it("checks a live row", () => {
    expect(liveProblems({ url: "" })).toContain("channels.live.err.url");
    expect(liveProblems({ url: "www.x.com" })).toContain("channels.live.err.urlFormat");
    expect(liveProblems({ url: "https://x", minutes: 0 })).toContain("channels.live.err.minutes");
    expect(liveProblems({ url: "https://x", start: "25:00" })).toContain("channels.slot.err.start");
    expect(liveProblems({ url: "https://x", start: "20:00", minutes: 10 })).toEqual([]);
  });
});

describe("filler weights", () => {
  it("turns weights into shares of the picks", () => {
    expect(
      shares([
        { pool: "a", weight: 300, cooldown_ms: 0 },
        { pool: "b", weight: 100, cooldown_ms: 0 },
      ])
    ).toEqual([75, 25]);
    expect(shares([{ pool: "a", weight: 0, cooldown_ms: 0 }])).toEqual([0]);
  });
  it("reads the plan's filler spec", () => {
    const spec = fillerSpec({
      filler: {
        lists: [{ pool: "news.topic", weight: 300, cooldown_ms: 60000 }, { bad: 1 }],
        repeat_cooldown_ms: 600000,
      },
    });
    expect(spec.lists).toEqual([{ pool: "news.topic", weight: 300, cooldown_ms: 60000 }]);
    expect(spec.repeat_cooldown_ms).toBe(600000);
    expect(fillerSpec({}).lists).toEqual([]);
  });
});

describe("reordering", () => {
  const ch = (id: string, number: number) => ({ id, number, name: id }) as Channel;
  it("moves an item", () => {
    expect(moveItem([1, 2, 3, 4], 3, 0)).toEqual([4, 1, 2, 3]);
    expect(moveItem([1, 2, 3], 0, 2)).toEqual([2, 3, 1]);
  });
  it("hands the same numbers out in the new order, sending only the changed", () => {
    const before = [ch("a", 101), ch("b", 102), ch("c", 105)];
    const after = moveItem(before, 2, 0);
    expect(renumber(before, after)).toEqual([
      { id: "c", number: 101 },
      { id: "a", number: 102 },
      { id: "b", number: 105 },
    ]);
    expect(renumber(before, before)).toEqual([]);
  });
});

describe("preview", () => {
  it("collapses consecutive filler into one row", () => {
    const p = (kind: Program["kind"], s: string, e: string, title = "x") =>
      ({ channel_id: "c", seq: 0, kind, title, start_at: s, end_at: e }) as Program;
    const rows = collapseFiller([
      p("program", "2026-01-01T10:00:00Z", "2026-01-01T10:25:00Z", "Ep 1"),
      p("filler", "2026-01-01T10:25:00Z", "2026-01-01T10:27:00Z"),
      p("bumper", "2026-01-01T10:27:00Z", "2026-01-01T10:30:00Z"),
      p("program", "2026-01-01T10:30:00Z", "2026-01-01T10:55:00Z", "Ep 2"),
    ]);
    expect(rows.map((r) => [r.filler, r.end])).toEqual([
      [false, "2026-01-01T10:25:00Z"],
      [true, "2026-01-01T10:30:00Z"],
      [false, "2026-01-01T10:55:00Z"],
    ]);
  });
});
