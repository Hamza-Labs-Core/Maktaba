// Cases from web/test-cases/queue-item-history.md (QIH-*), rendered through
// the shared page harness (RequireAuth + route error boundary + signed-in user).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

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
import { ApiError } from "../../lib/api";
import type { ItemHistoryData } from "../../lib/queueApi";
import { routeGet } from "../../test/queueHarness";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";
import {
  extensionsFixture,
  libraryHistoryFixture,
  snapshotFixture,
  videoHistoryFixture,
} from "../../test/fixtures/queues";

type Table = Record<string, unknown>;
const LIB = "/api/items/library/lib-1/history";

function table(over: Table = {}): Table {
  return {
    "/api/queues": snapshotFixture,
    [LIB]: libraryHistoryFixture,
    "/api/extensions": { extensions: extensionsFixture },
    "/api/reports": { reports: [] },
    "/api/items/library/lib-1/collections": { topics: [], clip_groups: [], promos: [] },
    ...over,
  };
}

const inserted = {
  results: [
    {
      subject_kind: "library",
      subject_id: "lib-1",
      stage: "detect",
      job_id: 9,
      outcome: "inserted",
    },
  ],
};

function setup(
  over: Table = {},
  opts: { path?: string; user?: typeof memberUser | null; history?: string[] } = {}
) {
  routeGet(get, table(over));
  return renderPage(<ItemHistory />, {
    path: opts.path ?? "/queue/items/library/lib-1",
    route: "/queue/items/:kind/:id",
    user: opts.user,
    history: opts.history,
    extraRoutes: (
      <>
        <Route path="/queue" element={<p>overview page</p>} />
        <Route path="/queue/:stage" element={<p>stage page</p>} />
      </>
    ),
  });
}

const apiErr = (status: number, title: string, detail?: string) =>
  new ApiError({ type: "about:blank", title, status, detail });
const calls = (path: string) => get.mock.calls.filter((c) => String(c[0]).split("?")[0] === path);
// the route error fallback (RouteErrorBoundary) says this; a load error does not
const noCrash = () =>
  expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();
const title = (name: RegExp) => screen.findByRole("heading", { level: 1, name });

function historyWith(path: string, mode: "null" | "missing"): ItemHistoryData {
  const d = structuredClone(libraryHistoryFixture) as unknown as Record<string, unknown>;
  const keys = path.split(".");
  let o: Record<string, unknown> = d;
  for (const k of keys.slice(0, -1)) o = o[k] as Record<string, unknown>;
  const last = keys[keys.length - 1];
  if (mode === "null") o[last] = null;
  else delete o[last];
  return d as unknown as ItemHistoryData;
}

describe("ItemHistory (test-cases/queue-item-history.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    post.mockResolvedValue(inserted);
    hub.reset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("QIH-02: video item: library crumb and Open link; no reports call", async () => {
    setup(
      { "/api/items/video/v-1/history": videoHistoryFixture },
      { path: "/queue/items/video/v-1" }
    );
    expect(await title(/Lecture 04/)).toBeInTheDocument();
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByRole("link", { name: /Islamic lectures/ })).toHaveAttribute(
      "href",
      "/queue/items/library/lib-1"
    );
    expect(screen.getByRole("link", { name: "Open in the library" })).toHaveAttribute(
      "href",
      "/videos/v-1"
    );
    expect(calls("/api/reports")).toHaveLength(0);
  });

  it("QIH-02: season item has no Open link; series item links to the series", async () => {
    const season = {
      ...videoHistoryFixture,
      subject: { ...videoHistoryFixture.subject, kind: "season", id: "s-3", label: "2024" },
    };
    const a = setup(
      { "/api/items/season/s-3/history": season },
      { path: "/queue/items/season/s-3" }
    );
    expect(await title(/2024/)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Open in the library" })).toBeNull();
    a.unmount();
    const series = {
      ...videoHistoryFixture,
      subject: { ...videoHistoryFixture.subject, kind: "series", id: "sr-1", label: "Seerah" },
    };
    setup({ "/api/items/series/sr-1/history": series }, { path: "/queue/items/series/sr-1" });
    expect(await title(/Seerah/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open in the library" })).toHaveAttribute(
      "href",
      "/series/sr-1"
    );
  });

  it("QIH-03: shows Loading while history is pending", async () => {
    setup({ [LIB]: new Promise(() => {}) });
    expect(await screen.findByRole("status")).toHaveTextContent("Loading…");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("QIH-04: unknown id shows 'Nothing is known about this item.'; back to the queues", async () => {
    setup(
      { "/api/items/video/nope/history": () => Promise.reject(apiErr(404, "Not Found")) },
      { path: "/queue/items/video/nope" }
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("Nothing is known about this item.");
    expect(screen.queryByRole("button", { name: /^Re-queue/ })).toBeNull();
    await userEvent.click(screen.getByRole("link", { name: "Back to the queues" }));
    expect(await screen.findByText("overview page")).toBeInTheDocument();
  });

  it("QIH-05: 500 / network / HTML is a load error with Try again, not 'nothing known'", async () => {
    const failures: Array<() => unknown> = [
      () => Promise.reject(apiErr(500, "Internal Server Error")),
      () => Promise.reject(new TypeError("Failed to fetch")),
      () => "<!doctype html><html></html>",
    ];
    for (const fail of failures) {
      let n = 0;
      const { unmount } = setup({
        [LIB]: () => (n++ === 0 ? fail() : libraryHistoryFixture),
      });
      const alert = await screen.findByRole("alert");
      expect(alert).not.toHaveTextContent("Nothing is known about this item.");
      expect(alert).toHaveTextContent("Something went wrong.");
      await userEvent.click(within(alert).getByRole("button", { name: "Try again" }));
      expect(await title(/Islamic lectures/)).toBeInTheDocument();
      unmount();
      get.mockReset();
    }
  });

  it.fails("QIH-05: a 401 on the history sends the user to /login", async () => {
    setup({ [LIB]: () => Promise.reject(apiErr(401, "Unauthorized")) });
    expect(await screen.findByText(/login page/, {}, { timeout: 300 })).toBeInTheDocument();
  });

  it("QIH-06: unknown kind: a 400 is an error, a 200 shows the kind text, not a key", async () => {
    const a = setup(
      {
        "/api/items/bogus/x/history": () =>
          Promise.reject(apiErr(400, "Bad Request", "unknown kind")),
      },
      { path: "/queue/items/bogus/x" }
    );
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    noCrash();
    a.unmount();
    vi.spyOn(console, "warn").mockImplementation(() => {});
    setup(
      {
        "/api/items/bogus/x/history": {
          ...videoHistoryFixture,
          subject: { ...videoHistoryFixture.subject, kind: "bogus", id: "x", label: "Odd" },
        },
      },
      { path: "/queue/items/bogus/x" }
    );
    expect(await title(/Odd/)).toBeInTheDocument();
    expect(screen.getByText("bogus")).toBeInTheDocument();
    expect(screen.queryByText("queue.subject.bogus")).toBeNull();
  });

  it.each([
    "kinds",
    "queues",
    "seasons",
    "appears_in",
    "timeline",
    "timeline.0.triggered",
    "timeline.0.reasons",
    "timeline.0.changes",
    "timeline.0.changes.0.items",
    "queues.0.last_job",
    "next",
  ])("QIH-07: history %s null or missing renders as empty", async (field) => {
    for (const mode of ["null", "missing"] as const) {
      const { unmount } = setup({ [LIB]: historyWith(field, mode) });
      expect(await title(/Islamic lectures/), mode).toBeInTheDocument();
      for (const f of ["Changes only", "Problems only", "Everything"]) {
        await userEvent.click(screen.getByRole("button", { name: f }));
      }
      noCrash();
      if (field === "appears_in")
        expect(screen.getByText("Not used anywhere yet.")).toBeInTheDocument();
      if (field === "timeline") expect(screen.getByText("Nothing to show.")).toBeInTheDocument();
      if (field === "queues")
        expect(screen.getByRole("button", { name: "Everything from the start" })).toBeVisible();
      if (field === "seasons") expect(screen.queryByRole("heading", { name: "Years" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Load older" })).toBeNull();
      unmount();
    }
  });

  it("QIH-08: extensions / reports / collections lists null do not crash", async () => {
    const variants: Table[] = [
      { "/api/extensions": { extensions: null } },
      { "/api/extensions": {} },
      { "/api/reports": { reports: null } },
      { "/api/reports": {} },
      { "/api/items/library/lib-1/collections": { topics: null, clip_groups: [], promos: [] } },
      { "/api/items/library/lib-1/collections": { topics: [], clip_groups: null, promos: [] } },
      { "/api/items/library/lib-1/collections": { topics: [], clip_groups: [], promos: null } },
      { "/api/items/library/lib-1/collections": {} },
    ];
    for (const v of variants) {
      const { unmount } = setup(v);
      expect(await title(/Islamic lectures/)).toBeInTheDocument();
      await waitFor(() => expect(calls("/api/items/library/lib-1/collections").length).toBe(1));
      noCrash();
      expect(screen.queryByRole("link", { name: "Review page" })).toBeNull();
      expect(screen.queryByText(/dz-12|dizqueTV/)).toBeNull();
      unmount();
      get.mockReset();
    }
  });

  it("QIH-08: a re-queue answered with results null shows a status, not a blank page", async () => {
    post.mockResolvedValue({ results: null });
    setup();
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Re-queue Place episodes" }));
    expect(
      await screen.findByText("0 queued, 0 added to jobs already waiting, 0 skipped.")
    ).toBeInTheDocument();
    noCrash();
  });

  it("QIH-09: valid empty history shows empty states and filters keep them", async () => {
    setup({
      [LIB]: {
        ...libraryHistoryFixture,
        kinds: [],
        queues: [],
        seasons: [],
        appears_in: [],
        timeline: [],
        next: null,
      },
    });
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    expect(screen.getByText("Not used anywhere yet.")).toBeInTheDocument();
    expect(screen.getByText("Nothing to show.")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /^(Seasons|Years)$/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Problems only" }));
    expect(screen.getByText("Nothing to show.")).toBeInTheDocument();
  });

  it("QIH-10: render crash shows the route error fallback; Back works", async () => {
    quietCrash();
    routeGet(
      get,
      table({
        [LIB]: {
          ...libraryHistoryFixture,
          appears_in: [
            { target: "channel_slot", title: "Wisdom", external_id: "ch-4", detail: { x: 1 } },
          ],
        },
      })
    );
    renderPage(<ItemHistory />, {
      path: "/queue/items/library/lib-1",
      route: "/queue/items/:kind/:id",
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await userEvent.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routeGet(get, table());
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
  });

  it("QIH-11: signed-out user is sent to /login", async () => {
    setup({}, { user: null });
    expect(
      await screen.findByText("login page (from /queue/items/library/lib-1)")
    ).toBeInTheDocument();
    expect(calls(LIB)).toHaveLength(0);
  });

  it("QIH-12: non-admin re-queue gets a readable 403; history not reloaded", async () => {
    post.mockRejectedValue(apiErr(403, "Forbidden", "admin only"));
    setup({}, { user: memberUser });
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    const loads = calls(LIB).length;
    await userEvent.click(screen.getByRole("button", { name: "Re-queue Place episodes" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Forbidden: admin only");
    await userEvent.click(screen.getByRole("button", { name: "Everything from the start" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Forbidden: admin only");
    expect(screen.queryByText(/queued, .* skipped\./)).toBeNull();
    expect(calls(LIB).length).toBe(loads);
  });

  it.fails("QIH-12: re-queue buttons are not offered to a non-admin", async () => {
    setup({}, { user: memberUser });
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    const btn = screen.queryByRole("button", { name: "Everything from the start" });
    expect(btn === null || (btn as HTMLButtonElement).disabled).toBe(true);
  });

  it("QIH-14: failed re-queue shows the message; the button leaves loading", async () => {
    setup();
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    post.mockRejectedValueOnce(apiErr(500, "Internal Server Error", "db down"));
    const btn = screen.getByRole("button", { name: "Re-queue Place episodes" });
    await userEvent.click(btn);
    expect(await screen.findByRole("alert")).toHaveTextContent("Internal Server Error: db down");
    post.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await userEvent.click(screen.getByRole("button", { name: "Re-queue Place episodes" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Failed to fetch");
    expect(screen.getByRole("button", { name: "Re-queue Place episodes" })).toBeEnabled();
  });

  it("QIH-17: Load older: failure keeps the button, success appends, null page is safe", async () => {
    let older = 0;
    const page2 = libraryHistoryFixture.timeline.map((e, i) => ({
      ...e,
      job: { ...e.job, id: 500 + i, subject_label: `Older ${i}` },
    }));
    setup({
      [LIB]: (url: string) => {
        if (!url.includes("cursor=c2")) return { ...libraryHistoryFixture, next: "c2" };
        older++;
        if (older === 1) return Promise.reject(apiErr(500, "Internal Server Error"));
        if (older === 2) return { ...libraryHistoryFixture, timeline: null, next: "c2" };
        return { ...libraryHistoryFixture, timeline: page2, next: null };
      },
    });
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    const entries = () => document.querySelectorAll(".mkt-q-timeline > li").length;
    const first = entries();
    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    expect(await screen.findByRole("button", { name: "Load older" })).toBeEnabled();
    expect(entries()).toBe(first);
    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    await waitFor(() => expect(older).toBe(2));
    noCrash();
    await userEvent.click(await screen.findByRole("button", { name: "Load older" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Load older" })).toBeNull());
    expect(entries()).toBe(first + page2.length);
  });

  it.todo(
    "QIH-18: a live reload after a finished job keeps the older pages already loaded (ItemHistory.tsx load() replaces data with page 1)"
  );

  it("QIH-21: a late answer for an item you left does not overwrite the current one", async () => {
    let resolveSeason: (v: unknown) => void = () => {};
    setup({ "/api/items/season/s-3/history": new Promise((r) => (resolveSeason = r)) });
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Re-queue Place episodes" }));
    expect(await screen.findByText(/1 queued/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("link", { name: /2024/ }));
    expect(await screen.findByRole("status")).toHaveTextContent("Loading…");
    expect(screen.queryByRole("heading", { level: 1, name: /Islamic lectures/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    expect(screen.queryByText(/1 queued/)).toBeNull();
    await act(async () =>
      resolveSeason({
        ...videoHistoryFixture,
        subject: { ...videoHistoryFixture.subject, kind: "season", id: "s-3", label: "2024" },
      })
    );
    expect(screen.getByRole("heading", { level: 1, name: /Islamic lectures/ })).toBeVisible();
  });

  it("QIH-21: a timeline stage chip goes to the stage page; Back returns", async () => {
    setup();
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
    const hist = screen.getByRole("heading", { name: "History" }).closest("section")!;
    const stageLink = within(hist)
      .getAllByRole("link")
      .find((a) => /^\/queue\/[^/]+$/.test(a.getAttribute("href") ?? ""));
    expect(stageLink).toBeDefined();
    await userEvent.click(stageLink!);
    expect(await screen.findByText("stage page")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await title(/Islamic lectures/)).toBeInTheDocument();
  });

  it("QIH-22: Choose queues… dialog is prefilled with this item and reloads on done", async () => {
    setup(
      { "/api/items/video/v-1/history": videoHistoryFixture },
      { path: "/queue/items/video/v-1" }
    );
    expect(await title(/Lecture 04/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Choose queues…" }));
    const dlg = await screen.findByRole("dialog", { name: "Queue work" });
    expect(within(dlg).getByRole("checkbox", { name: /Lecture 04/ })).toBeChecked();
    expect(within(dlg).queryByRole("checkbox", { name: /Islamic lectures/ })).toBeNull();
    // Place episodes runs on libraries only: nothing to queue for a video
    await userEvent.click(within(dlg).getByRole("checkbox", { name: "Place episodes" }));
    await userEvent.click(within(dlg).getByRole("button", { name: /^Queue work \(/ }));
    expect(await within(dlg).findByRole("alert")).toHaveTextContent(
      "None of the chosen queues work on the chosen items."
    );
    await userEvent.click(within(dlg).getByRole("checkbox", { name: "Place episodes" }));
    await userEvent.click(within(dlg).getByRole("checkbox", { name: "Length" }));
    const loads = calls("/api/items/video/v-1/history").length;
    await userEvent.click(within(dlg).getByRole("button", { name: /^Queue work \(/ }));
    expect(post).toHaveBeenCalledWith(
      "/api/queue/retrigger",
      expect.objectContaining({
        items: [{ subject_kind: "video", subject_id: "v-1", stage: "probe" }],
      })
    );
    await userEvent.click(await within(dlg).findByRole("button", { name: "Done" }));
    await waitFor(() =>
      expect(calls("/api/items/video/v-1/history").length).toBeGreaterThan(loads)
    );
  });
});
