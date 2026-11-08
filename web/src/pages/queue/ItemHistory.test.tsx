import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, hub } = vi.hoisted(() => {
  const channels = new Map<string, Set<(ev: unknown) => void>>();
  return {
    get: vi.fn(),
    post: vi.fn(),
    hub: {
      subscribe: (ch: string, fn: (ev: unknown) => void) => {
        if (!channels.has(ch)) channels.set(ch, new Set());
        channels.get(ch)!.add(fn);
        return () => channels.get(ch)?.delete(fn);
      },
      emit: (ch: string, ev: unknown) => channels.get(ch)?.forEach((f) => f(ev)),
      reset: () => channels.clear(),
    },
  };
});
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("../../lib/ws", () => ({ subscribe: hub.subscribe }));

import { ItemHistory } from "./ItemHistory";
import type { AppearsInEntry } from "../../lib/queueApi";
import { renderAt, routeGet } from "../../test/queueHarness";
import { events, jobRef, libraryHistoryFixture, snapshotFixture } from "../../test/fixtures/queues";

function setup() {
  routeGet(get, {
    "/api/queues": snapshotFixture,
    "/api/items/library/lib-1/history": libraryHistoryFixture,
  });
  post.mockResolvedValue({
    results: [
      {
        subject_kind: "library",
        subject_id: "lib-1",
        stage: "detect",
        job_id: 9,
        outcome: "inserted",
      },
    ],
  });
  return renderAt(<ItemHistory />, "/queue/items/library/lib-1", "/queue/items/:kind/:id");
}

describe("ItemHistory", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
    hub.reset();
  });

  it("renders title, kind chips, seasons as bars and the timeline", async () => {
    setup();
    expect(
      await screen.findByRole("heading", { level: 1, name: /Islamic lectures/ })
    ).toBeInTheDocument();
    const kinds = screen.getByRole("list", { name: "What kind of item" });
    expect(within(kinds).getByText("talk")).toBeInTheDocument();
    expect(within(kinds).getByText("ongoing")).toBeInTheDocument();
    // ongoing shows years
    expect(screen.getByRole("heading", { name: "Years" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /2023/ })).toHaveAttribute(
      "href",
      "/queue/items/season/s-2"
    );
    expect(screen.getByText("→", { exact: false })).toBeInTheDocument();
    expect(screen.getByText(/Re-queued:/)).toHaveTextContent("Tag topics");
  });

  it("shows what an automatic write changed", async () => {
    setup();
    const list = await screen.findByRole("list", { name: "What this write changed" });
    expect(within(list).getByText(/Created channel/)).toBeInTheDocument();
    expect(within(list).getByText(/Added 3 slots to/)).toHaveTextContent("Lecture 22");
    expect(within(list).getByText(/Removed 1 slots from/)).toBeInTheDocument();
    expect(within(list).getByText(/Reordered 24 slots in/)).toBeInTheDocument();
  });

  it("filters the history: changes only / problems only", async () => {
    setup();
    await screen.findByRole("list", { name: "What this write changed" });
    await userEvent.click(screen.getByRole("button", { name: "Problems only" }));
    expect(screen.getByText("youtube_bot_check")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "What this write changed" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Changes only" }));
    expect(screen.queryByText("youtube_bot_check")).toBeNull();
    expect(screen.getByRole("list", { name: "What this write changed" })).toBeInTheDocument();
  });

  it("re-queues one queue, and everything from the start with the note", async () => {
    setup();
    await userEvent.click(await screen.findByRole("button", { name: "Re-queue Place episodes" }));
    expect(post).toHaveBeenCalledWith("/api/queue/retrigger", {
      items: [{ subject_kind: "library", subject_id: "lib-1", stage: "detect" }],
      mode: "queue",
    });
    expect(
      await screen.findByText(/1 queued, 0 added to jobs already waiting/)
    ).toBeInTheDocument();
    expect(
      screen.getByText("This only queues the jobs; what changes then flows on by itself.")
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Everything from the start" }));
    expect(post).toHaveBeenLastCalledWith("/api/queue/retrigger", {
      items: [{ subject_kind: "library", subject_id: "lib-1" }],
      mode: "force_cascade",
    });
  });

  it("disables re-queue for queues that cannot run on this item", async () => {
    setup();
    expect(await screen.findByRole("button", { name: "Re-queue Plan channels" })).toBeDisabled();
  });

  it("lists the channels and slots it appears in, and drops targets it does not know", async () => {
    routeGet(get, {
      "/api/queues": snapshotFixture,
      "/api/items/library/lib-1/history": {
        ...libraryHistoryFixture,
        appears_in: [
          ...libraryHistoryFixture.appears_in,
          // a row of a target this web has no words for (an integration since removed)
          {
            target: "retired_target" as AppearsInEntry["target"],
            title: "Old collection",
            external_id: "old-991",
          },
        ],
      },
    });
    renderAt(<ItemHistory />, "/queue/items/library/lib-1", "/queue/items/:kind/:id");
    const where = (await screen.findByRole("heading", { name: "Where it appears" })).closest(
      "section"
    )!;
    await within(where).findByText("Channel slot");
    expect(within(where).getByText("Maktaba channel")).toBeInTheDocument();
    expect(within(where).queryByText("Old collection")).toBeNull();
  });

  it("refreshes when a job for this item finishes on the stream", async () => {
    setup();
    await screen.findByRole("heading", { level: 1, name: /Islamic lectures/ });
    const calls = () => get.mock.calls.filter((c) => String(c[0]).includes("/history")).length;
    expect(calls()).toBe(1);
    act(() =>
      hub.emit(
        "jobs",
        events.jobFinished(
          jobRef({ id: 99, stage: "detect", subject_kind: "library", subject_id: "lib-1" })
        )
      )
    );
    await vi.waitFor(() => expect(calls()).toBeGreaterThanOrEqual(1), { timeout: 2500 });
  });
  it("shows the library's topics, clip groups and promos and links its review page", async () => {
    routeGet(get, {
      "/api/queues": snapshotFixture,
      "/api/items/library/lib-1/history": libraryHistoryFixture,
      "/api/items/library/lib-1/collections": {
        topics: [{ id: "t1", name: "السيرة", items: 12 }],
        clip_groups: [{ id: "g1", kind: "clips", basis: "topic", title: "مقاطع", items: 40 }],
        promos: [
          {
            video_id: "v1",
            title: "برومو الحبيب",
            show: "الحبيب",
            season_id: "s1",
            season: 2,
            evidence: "title names the show",
          },
        ],
      },
      "/api/reports": {
        reports: [
          {
            name: "lib-1.html",
            kind: "review",
            title: "Islamic lectures",
            library_id: "lib-1",
            size: 1,
            updated_at: "2026-05-18T11:00:00Z",
          },
        ],
      },
    });
    renderAt(<ItemHistory />, "/queue/items/library/lib-1", "/queue/items/:kind/:id");
    const panel = (
      await screen.findByRole("heading", { name: "Topics, clips and promos" })
    ).closest("section")!;
    expect(await within(panel).findByText(/السيرة/)).toBeInTheDocument();
    expect(within(panel).getByText(/^\W*مقاطع\W*$/)).toBeInTheDocument();
    expect(within(panel).queryByText("not written")).toBeNull();
    expect(within(panel).getByRole("link", { name: /الحبيب.? S2/ })).toHaveAttribute(
      "href",
      "/queue/items/season/s1"
    );
    expect(within(panel).getByRole("link", { name: /برومو الحبيب/ })).toHaveAttribute(
      "href",
      "/videos/v1"
    );
    expect(await screen.findByRole("link", { name: "Review page" })).toHaveAttribute(
      "href",
      "/queue/reports?open=lib-1.html"
    );
  });

  it("has no collections panel or review link when there is nothing to show", async () => {
    setup();
    await screen.findByRole("heading", { level: 1, name: /Islamic lectures/ });
    expect(screen.queryByRole("heading", { name: "Topics, clips and promos" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Review page" })).toBeNull();
  });
});
