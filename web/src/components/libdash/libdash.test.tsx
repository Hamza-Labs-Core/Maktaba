// The library dashboard's parts: a library's health (state, reasons, the
// attention order), the filters with their counts, the sorts (Arabic names
// collate in Arabic order), the activity series, the charts' words and tables,
// the stage list's folding, the tile and the Queues page's attention panel.
import { describe, it, expect, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderAt } from "../../test/queueHarness";
import { axeViolations } from "../../test/axe";
import { librariesFixture, snapshotFixture } from "../../test/fixtures/queues";
import { summary } from "../../test/fixtures/offline";
import type { LibraryQueueRow } from "../../lib/queueApi";
import {
  filterCounts,
  filterLibraries,
  libraryHealth,
  sortLibraries,
  youtubeState,
} from "./health";
import { activityById, activityOf } from "./useLibraryBoard";
import { ActivityChart, StatusBar } from "./charts";
import { StageChart, stageNeedsWork } from "./StageChart";
import { LibraryTile, libraryPath } from "./LibraryTile";
import { LibrariesAttention, ATTENTION_SHOWN } from "./LibrariesAttention";

const cell = (
  stage: string,
  done: number,
  total: number,
  running = 0,
  pending = 0,
  failed = 0
) => ({
  stage,
  done,
  total,
  running,
  pending,
  failed,
});
function lib(
  id: string,
  name: string,
  strip: LibraryQueueRow["strip"],
  over: Partial<LibraryQueueRow> = {}
) {
  return { library_id: id, name, videos: 100, seasons: 2, strip, last_activity_at: null, ...over };
}
const done = (id: string, name: string, over: Partial<LibraryQueueRow> = {}) =>
  lib(id, name, [cell("probe", 10, 10)], over);
const failing = (id: string, name: string, failed = 3) =>
  lib(id, name, [cell("probe", 5, 10, 0, 2, failed), cell("link", 10, 10, 0, 0, 1)]);

describe("libraryHealth", () => {
  it("sums the stages, floors the percentage and names the state", () => {
    const h = libraryHealth(lib("a", "A", [cell("probe", 1999, 2000), cell("link", 5, 0)]));
    // a stage with nothing to do adds nothing; 1999 of 2000 is 99 %, never 100
    expect([h.done, h.total, h.pct, h.state]).toEqual([1999, 2000, 99, "done"]);
    expect(libraryHealth(failing("b", "B")).state).toBe("failing");
    expect(libraryHealth(failing("b", "B")).failingStages).toBe(2);
    expect(libraryHealth(lib("c", "C", [cell("probe", 1, 5, 1, 3)])).state).toBe("working");
    expect(libraryHealth(lib("d", "D", [cell("probe", 1, 5, 0, 3)])).state).toBe("waiting");
    expect(libraryHealth(lib("e", "E", [])).state).toBe("empty");
    expect(libraryHealth({ ...lib("f", "F", []), strip: null as never }).state).toBe("empty");
  });

  it("reads YouTube as none, off, on or a problem, and nothing without the summary", () => {
    expect(youtubeState(undefined)).toBeNull();
    expect(youtubeState(summary({ youtube: { ...summary().youtube, sources: 0 } }))).toBe("none");
    expect(youtubeState(summary({ youtube: { ...summary().youtube, sources_on: 0 } }))).toBe("off");
    expect(youtubeState(summary())).toBe("on");
    expect(
      youtubeState(
        summary({
          youtube: {
            ...summary().youtube,
            problems: [{ source_id: "s", url: "u", error: "e" }] as never,
          },
        })
      )
    ).toBe("problem");
  });

  it("lists what needs an admin, most urgent first", () => {
    const h = libraryHealth(failing("a", "A"), summary());
    expect(h.reasons).toEqual(["failing", "duplicates", "convert"]);
    expect(h.tier).toBe(0);
    const calm = libraryHealth(done("b", "B"), summary({ not_mp4: 0, duplicates: 0 }));
    expect(calm.reasons).toEqual([]);
    expect(calm.tier).toBe(5);
  });
});

describe("filters and sorts", () => {
  const off = summary({
    youtube: { ...summary().youtube, sources_on: 0 },
    not_mp4: 0,
    duplicates: 0,
  });
  const list = [
    libraryHealth(failing("f", "محاضرات", 9)),
    libraryHealth(done("c", "Kids"), summary({ duplicates: 0 })),
    libraryHealth(done("y", "قصة وآية", { videos: 900 }), off),
    libraryHealth(done("q", "Archive"), summary({ not_mp4: 0, duplicates: 0 })),
  ];

  it("counts each filter and filters by name, diacritics aside", () => {
    expect(filterCounts(list)).toEqual({
      all: 4,
      attention: 2,
      failing: 1,
      convert: 1,
      youtubeOff: 1,
    });
    expect(filterLibraries(list, "failing", "").map((h) => h.id)).toEqual(["f"]);
    expect(filterLibraries(list, "youtubeOff", "").map((h) => h.id)).toEqual(["y"]);
    expect(filterLibraries(list, "all", "مُحاضَرات").map((h) => h.id)).toEqual(["f"]);
    expect(filterLibraries(list, "all", "  KIDS ").map((h) => h.id)).toEqual(["c"]);
    expect(filterLibraries(list, "failing", "Kids")).toEqual([]);
  });

  it("sorts by attention, name (in the page's language), failures, size and change", () => {
    expect(sortLibraries(list, "attention", "en").map((h) => h.id)).toEqual(["f", "c", "q", "y"]);
    expect(sortLibraries(list, "name", "en").map((h) => h.id)).toEqual(["q", "c", "y", "f"]);
    // Arabic order: قصة (ق) before محاضرات (م)
    expect(
      sortLibraries(list, "name", "ar")
        .filter((h) => h.id === "f" || h.id === "y")
        .map((h) => h.id)
    ).toEqual(["y", "f"]);
    expect(sortLibraries(list, "failing", "en")[0].id).toBe("f");
    expect(sortLibraries(list, "size", "en")[0].id).toBe("y");
    const recent = [
      libraryHealth(done("old", "Old", { last_activity_at: "2026-10-01T00:00:00Z" })),
      libraryHealth(done("new", "New", { last_activity_at: "2026-11-01T00:00:00Z" })),
      libraryHealth(done("never", "Never")),
    ];
    expect(sortLibraries(recent, "recent", "en").map((h) => h.id)).toEqual(["new", "old", "never"]);
  });
});

describe("activity", () => {
  it("lines each library's days up with the window and gives the rest flat days", () => {
    const m = activityById({
      days: ["2026-11-01", "2026-11-02"],
      items: [{ library_id: "a", done: [4], failed: [0, 2] }],
    });
    expect(activityOf(m, "a")).toEqual({
      days: ["2026-11-01", "2026-11-02"],
      done: [4, 0],
      failed: [0, 2],
    });
    expect(activityOf(m, "zzz")).toEqual({
      days: ["2026-11-01", "2026-11-02"],
      done: [0, 0],
      failed: [0, 0],
    });
    expect(activityOf(null, "a")).toBeUndefined();
    expect(activityOf(activityById(null), "a")).toEqual({ days: [], done: [], failed: [] });
  });
});

describe("charts", () => {
  it("the activity chart names itself and has its numbers as a table", async () => {
    const { container } = renderAt(
      <ActivityChart
        name="Kids"
        days={["2026-11-01", "2026-11-02"]}
        done={[12, 3]}
        failed={[1, 0]}
      />,
      "/"
    );
    expect(
      screen.getByRole("img", {
        name: /Kids: jobs finished per day over 2 days, 15 done and 1 failed/,
      })
    ).toBeInTheDocument();
    const table = screen.getByRole("table", { hidden: true });
    expect(within(table).getAllByRole("row", { hidden: true })).toHaveLength(3);
    expect(within(table).getByText("12")).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("the compact chart is one named picture", () => {
    renderAt(
      <ActivityChart compact name="Kids" days={["2026-11-01"]} done={[2]} failed={[0]} />,
      "/"
    );
    expect(screen.getByRole("img", { name: /Kids/ })).toBeInTheDocument();
    expect(screen.queryByRole("table", { hidden: true })).toBeNull();
  });

  it("a status bar says its counts in words", () => {
    renderAt(
      <StatusBar
        c={{ done: 5, total: 10, running: 1, waiting: 3, failed: 1 }}
        label="five of ten"
      />,
      "/"
    );
    expect(screen.getByRole("img", { name: "five of ten" })).toBeInTheDocument();
  });

  it("the stage list puts failures first and folds the finished queues", async () => {
    const row = librariesFixture[0];
    renderAt(<StageChart row={row} stages={snapshotFixture.stages} />, "/");
    expect(stageNeedsWork(cell("x", 5, 5))).toBe(false);
    expect(stageNeedsWork(cell("x", 5, 5, 0, 0, 1))).toBe(true);
    const names = () => screen.getAllByRole("link").map((a) => a.getAttribute("href"));
    // link has 7 failed, fingerprint 1, probe 0 failed but work left
    expect(names()).toEqual(["/queue/link", "/queue/fingerprint", "/queue/probe"]);
    await userEvent.click(screen.getByRole("button", { name: "Show the 2 finished queues" }));
    expect(names()).toHaveLength(5);
    await userEvent.click(screen.getByRole("button", { name: "Hide the finished queues" }));
    expect(names()).toHaveLength(3);
  });

  it("a library with every queue done says so", () => {
    renderAt(<StageChart row={done("a", "A")} stages={[]} />, "/");
    expect(screen.getByText("All 1 queues are done.")).toBeInTheDocument();
  });
});

describe("LibraryTile", () => {
  const h = libraryHealth(
    failing("lib 1", "Islamic lectures"),
    summary({ youtube: { ...summary().youtube, sources_on: 0 } })
  );
  const activity = { days: ["2026-11-01", "2026-11-02"], done: [4, 6], failed: [0, 1] };

  it("shows progress, failures, YouTube off, files to convert and duplicates, each one step away", async () => {
    const onSources = vi.fn();
    const onSelect = vi.fn();
    const { container } = renderAt(
      <ul>
        <LibraryTile h={h} activity={activity} onSelect={onSelect} onSources={onSources} />
      </ul>,
      "/"
    );
    expect(screen.getByRole("link", { name: /Islamic lectures/ })).toHaveAttribute(
      "href",
      libraryPath("lib 1")
    );
    expect(libraryPath("lib 1", "convert")).toBe("/admin/libraries/lib%201#convert");
    expect(screen.getByText("4 failed")).toBeInTheDocument();
    expect(screen.getByText("Queues failing: 2")).toBeInTheDocument();
    expect(screen.getByText("10 done in 2 days")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /37 to convert/ })).toHaveAttribute(
      "href",
      "/admin/libraries/lib%201#convert"
    );
    expect(screen.getByRole("link", { name: "2 duplicates to review" })).toHaveAttribute(
      "href",
      "/queue/duplicates?library=lib%201"
    );
    await userEvent.click(
      screen.getByRole("button", {
        name: "YouTube sources of Islamic lectures: all 3 off. Open them",
      })
    );
    expect(onSources).toHaveBeenCalled();
    await userEvent.click(screen.getByRole("checkbox", { name: "Select Islamic lectures" }));
    expect(onSelect).toHaveBeenCalledWith(true);
    expect(await axeViolations(container)).toEqual([]);
  });

  it("without the admin's handlers has no checkbox and a plain YouTube badge", () => {
    renderAt(
      <ul>
        <LibraryTile h={h} />
      </ul>,
      "/"
    );
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText("YouTube off")).toBeInTheDocument();
  });

  it("a library without the summary shows no badges", () => {
    renderAt(
      <ul>
        <LibraryTile h={libraryHealth(done("a", "Kids"))} />
      </ul>,
      "/"
    );
    expect(screen.queryByRole("list", { name: /What Kids needs/ })).toBeNull();
    expect(screen.getByText("Up to date")).toBeInTheDocument();
  });
});

describe("LibrariesAttention", () => {
  it("lists the libraries needing attention, most urgent first, and links the rest", () => {
    const rows = [
      done("calm", "Calm"),
      ...Array.from({ length: ATTENTION_SHOWN + 2 }, (_, i) =>
        failing(`f${i}`, `Failing ${i}`, i + 1)
      ),
    ];
    renderAt(<LibrariesAttention rows={rows} />, "/queue");
    const panel = screen.getByRole("region", { name: "Libraries" });
    expect(within(panel).getByText("8 of 9 libraries need attention.")).toBeInTheDocument();
    const links = within(panel).getAllByRole("link", { name: /Failing/ });
    expect(links).toHaveLength(ATTENTION_SHOWN);
    // most failures first
    expect(links[0]).toHaveAttribute("href", "/admin/libraries/f7#queues");
    expect(within(panel).getByRole("link", { name: "2 more on the dashboard" })).toHaveAttribute(
      "href",
      "/queue/libraries?show=attention"
    );
  });

  it("with the summaries shows why, and says when nothing needs attention", () => {
    const { unmount } = renderAt(
      <LibrariesAttention
        rows={[done("a", "Kids")]}
        offline={{ a: summary({ duplicates: 0, not_mp4: 5 }) }}
      />,
      "/queue"
    );
    expect(screen.getByText("5 to convert")).toBeInTheDocument();
    unmount();
    renderAt(<LibrariesAttention rows={[done("a", "Kids")]} />, "/queue");
    expect(screen.getByText("All 1 libraries are up to date.")).toBeInTheDocument();
  });
});
