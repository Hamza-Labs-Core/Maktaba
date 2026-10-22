// Cases from web/test-cases/queue-overview.md (QOV-*), rendered through the
// shared page harness (RequireAuth + route error boundary + signed-in user).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

const { get, post, put, patch, hub } = vi.hoisted(() => {
  const channels = new Map<string, Set<(ev: unknown) => void>>();
  return {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    patch: vi.fn(),
    hub: {
      subscribe: (ch: string, fn: (ev: unknown) => void) => {
        if (!channels.has(ch)) channels.set(ch, new Set());
        channels.get(ch)!.add(fn);
        return () => channels.get(ch)?.delete(fn);
      },
      emit: (ch: string, ev: unknown) => channels.get(ch)?.forEach((f) => f(ev)),
      count: (ch: string) => channels.get(ch)?.size ?? 0,
      reset: () => channels.clear(),
    },
  };
});
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch } };
});
vi.mock("../../lib/ws", () => ({ subscribe: hub.subscribe }));

import { QueueOverview } from "./QueueOverview";
import { ApiError } from "../../lib/api";
import { clusterSocket } from "../../lib/clusterSocket";
import { FakeSocket } from "../../test/fakeSocket";
import { routeGet } from "../../test/queueHarness";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";
import {
  events,
  extensionsFixture,
  failureGroupsFixture,
  healthSeriesFixture,
  librariesFixture,
  logFixture,
  snapshotFixture,
  workersFixture,
} from "../../test/fixtures/queues";

const pulse = {
  state: "working",
  running: 2,
  waiting: 4,
  paused_all: false,
  last_activity_at: "2026-05-18T11:59:50Z",
  age_sec: 9,
  stuck: [],
};
const insights = {
  totals: {
    libraries: 2,
    videos: 321,
    videos_with_length: 300,
    series: 5,
    seasons: 9,
    plex_collections: 4,
    dizquetv_shows: 1,
    channels: 3,
    style_pinned: 0,
    youtube_videos: 0,
    videos_without_exact_date: 0,
  },
  styles: { talk: 6 },
  genres: [{ genre: "fiqh", seasons: 4 }],
};

type Table = Record<string, unknown>;

function table(over: Table = {}): Table {
  return {
    "/api/queues": snapshotFixture,
    "/api/issues": { groups: failureGroupsFixture },
    "/api/queues/libraries": { items: librariesFixture },
    "/api/queues/health": { series: healthSeriesFixture },
    "/api/workers": { workers: workersFixture },
    "/api/extensions": { extensions: extensionsFixture },
    "/api/activity": { items: logFixture, next: null },
    "/api/queues/pulse": pulse,
    "/api/queues/insights": insights,
    "/api/queues/running": { host: "home", jobs: [] },
    ...over,
  };
}

function setup(over: Table = {}, opts: { path?: string; user?: typeof memberUser | null } = {}) {
  routeGet(get, table(over));
  return renderPage(<QueueOverview />, {
    path: opts.path ?? "/queue",
    route: "/queue",
    user: opts.user,
  });
}

const forbidden = () =>
  new ApiError({ type: "about:blank", title: "Forbidden", status: 403, detail: "admin only" });
const calls = (path: string) => get.mock.calls.filter((c) => String(c[0]).split("?")[0] === path);
const pageReady = () => screen.findByRole("link", { name: "Length" });
// the route error fallback (RouteErrorBoundary) says this; a load error does not
const noCrash = () =>
  expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();

/** a copy of the snapshot fixture with one (possibly nested) key set to null or removed */
function withField(path: string, mode: "null" | "missing") {
  const s = structuredClone(snapshotFixture) as unknown as Record<string, unknown>;
  const keys = path.split(".");
  let o: Record<string, unknown> = s;
  for (const k of keys.slice(0, -1)) o = o[k] as Record<string, unknown>;
  const last = keys[keys.length - 1];
  if (mode === "null") o[last] = null;
  else delete o[last];
  return s;
}

describe("QueueOverview (test-cases/queue-overview.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    put.mockReset();
    patch.mockReset();
    post.mockResolvedValue(undefined);
    hub.reset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("QOV-02: shows Loading while the first snapshot is pending, then the page", async () => {
    let resolve: (v: unknown) => void = () => {};
    setup({ "/api/queues": new Promise((r) => (resolve = r)) });
    expect(await screen.findByRole("status")).toHaveTextContent("Loading…");
    expect(screen.queryByRole("button", { name: "Pause all" })).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    await act(async () => resolve(snapshotFixture));
    expect(await pageReady()).toBeInTheDocument();
  });

  it("QOV-03: snapshot load fails, then Try again recovers", async () => {
    const fails = [
      new ApiError({ type: "about:blank", title: "Internal Server Error", status: 500 }),
      new TypeError("Failed to fetch"),
    ];
    for (const err of fails) {
      let n = 0;
      const { unmount } = setup({
        "/api/queues": () => (n++ === 0 ? Promise.reject(err) : snapshotFixture),
      });
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent("The queues could not be loaded.");
      await userEvent.click(within(alert).getByRole("button", { name: "Try again" }));
      expect(await pageReady()).toBeInTheDocument();
      expect(screen.queryByText("The queues could not be loaded.")).toBeNull();
      expect(calls("/api/queues").length).toBeGreaterThanOrEqual(2);
      unmount();
      get.mockReset();
    }
  });

  it("QOV-04: 401 / 403 on the snapshot shows the error, not a blank page", async () => {
    for (const status of [401, 403]) {
      const { unmount } = setup({
        "/api/queues": () =>
          Promise.reject(new ApiError({ type: "about:blank", title: "Unauthorized", status })),
      });
      const alert = await screen.findByRole("alert");
      expect(alert).toHaveTextContent("The queues could not be loaded.");
      expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
      unmount();
    }
  });

  it("QOV-05: malformed or non-JSON snapshot shows the load error, no crash", async () => {
    // (a) truncated JSON: api.get rejects with the JSON parse error
    const a = setup({
      "/api/queues": () => Promise.reject(new SyntaxError("Unexpected end of JSON input")),
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("The queues could not be loaded.");
    a.unmount();
    // (b) a proxy's HTML page: request() returns the text as a string
    setup({ "/api/queues": "<!doctype html><html><body>app</body></html>" });
    expect(await screen.findByRole("alert")).toHaveTextContent("The queues could not be loaded.");
    noCrash();
  });

  it.each([
    "stages",
    "queues",
    "pools",
    "flows",
    "health.spark.nas_open_ms",
    "health.spark.cpu_pct",
    "health.spark.backlog_ready",
    "queues.0.running_jobs",
    "queues.0.served_by",
    "stages.0.edges",
    "stages.0.subject_kinds",
    "pools.0.stages",
  ])("QOV-06: /api/queues %s null or missing renders as empty", async (field) => {
    for (const mode of ["null", "missing"] as const) {
      const { unmount } = setup({
        "/api/queues": withField(field, mode),
        "/api/queues/health": { series: [] },
      });
      expect(
        await screen.findByRole("heading", { level: 1, name: "Processing queues" }),
        mode
      ).toBeInTheDocument();
      expect(screen.getByRole("heading", { name: "Needs attention" })).toBeInTheDocument();
      noCrash();
      unmount();
    }
  });

  it("QOV-07: /api/issues groups / sample_subjects null or missing", async () => {
    for (const issues of [{ groups: null }, {}]) {
      const { unmount } = setup({ "/api/issues": issues });
      await pageReady();
      const sec = screen.getByRole("heading", { name: "Needs attention" }).closest("section")!;
      expect(within(sec).getByText("Nothing needs attention.")).toBeInTheDocument();
      noCrash();
      unmount();
    }
    setup({ "/api/issues": { groups: [{ ...failureGroupsFixture[0], sample_subjects: null }] } });
    expect(await screen.findByText("NAS read timed out")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Lecture 04/ })).toBeNull();
    noCrash();
  });

  it("QOV-08: /api/queues/libraries items / strip null or missing", async () => {
    for (const libs of [{ items: null }, {}]) {
      const { unmount } = setup({ "/api/queues/libraries": libs });
      await pageReady();
      expect(screen.getByText("No libraries match.")).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Queue work…" }));
      const dlg = await screen.findByRole("dialog", { name: "Queue work" });
      expect(within(dlg).queryByRole("checkbox", { name: /Islamic lectures/ })).toBeNull();
      noCrash();
      unmount();
    }
    setup({ "/api/queues/libraries": { items: [{ ...librariesFixture[0], strip: null }] } });
    expect(await screen.findByRole("link", { name: /Islamic lectures/ })).toBeInTheDocument();
    noCrash();
  });

  it("QOV-09: /api/workers workers null, missing or empty", async () => {
    for (const w of [{ workers: null }, {}]) {
      const { unmount } = setup({ "/api/workers": w });
      await pageReady();
      await waitFor(() => expect(calls("/api/workers").length).toBeGreaterThan(0));
      expect(screen.queryByRole("region", { name: "Workers" })).toBeNull();
      noCrash();
      unmount();
    }
    const host = setup({ "/api/workers": {} }, { path: "/queue?host=home" });
    await pageReady();
    expect(
      await screen.findByText(/Nothing is running on .*this home.* right now\./)
    ).toBeVisible();
    noCrash();
    host.unmount();
    setup({ "/api/workers": { workers: [] } });
    expect(await screen.findByText("No workers have reported in.")).toBeInTheDocument();
  });

  it("QOV-09: a hub workers message with workers null is ignored", async () => {
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    clusterSocket.reset();
    try {
      setup();
      const ws = FakeSocket.last;
      ws.open();
      ws.recv({ type: "queues", data: snapshotFixture });
      await pageReady();
      ws.recv({ type: "workers", data: { workers: workersFixture } });
      expect(await screen.findByText(/NAS, detection, Plex/)).toBeInTheDocument();
      ws.recv({ type: "workers", data: { workers: null } });
      expect(screen.getByText(/NAS, detection, Plex/)).toBeInTheDocument();
      noCrash();
    } finally {
      clusterSocket.reset();
      vi.unstubAllGlobals();
    }
  });

  it("QOV-10: side panels' list fields null or missing show empty states", async () => {
    const variants: Table[] = [
      { "/api/queues/health": { series: null } },
      { "/api/queues/health": { series: [{ metric: "cpu.pct", points: null }] } },
      { "/api/queues/health": {} },
      { "/api/queues/pulse": { ...pulse, state: "stalled", stuck: null } },
      { "/api/queues/pulse": { ...pulse, stuck: undefined } },
      { "/api/queues/insights": { ...insights, genres: null } },
      { "/api/queues/insights": { ...insights, styles: null } },
      { "/api/queues/insights": { ...insights, totals: null } },
      { "/api/queues/insights": { totals: insights.totals } },
      { "/api/extensions": { extensions: null } },
      { "/api/extensions": {} },
    ];
    for (const v of variants) {
      const { unmount } = setup(v);
      await pageReady();
      expect(screen.getByRole("group", { name: "CPU" })).toBeInTheDocument();
      noCrash();
      unmount();
    }
  });

  it("QOV-10: Activity with items null shows 'No activity yet.', not Loading", async () => {
    setup({ "/api/activity": { items: null, next: null } });
    await pageReady();
    const log = screen.getByRole("heading", { name: "Activity" }).closest("section")!;
    expect(await within(log).findByText("No activity yet.")).toBeInTheDocument();
  });

  it("QOV-10: host panel with jobs null shows the nothing-running text", async () => {
    setup({ "/api/queues/running": { host: "home", jobs: null } }, { path: "/queue?host=home" });
    expect(
      await screen.findByText(/Nothing is running on .*this home.* right now\./)
    ).toBeInTheDocument();
  });

  it("QOV-10: Explain with videos / remote null shows 'nothing matches'", async () => {
    setup({ "/api/explain": { q: "lecture", videos: null, remote: null, limit: 20 } });
    await pageReady();
    await userEvent.type(screen.getByRole("searchbox", { name: "Title text" }), "lecture");
    await userEvent.click(screen.getByRole("button", { name: "Explain" }));
    expect(await screen.findByText("No video matches that text.")).toBeInTheDocument();
    noCrash();
  });

  it("QOV-10: Style panel with items null shows 'No seasons match.'", async () => {
    setup({ "/api/seasons/styles": { items: null } });
    await pageReady();
    await userEvent.click(screen.getByRole("button", { name: "Find seasons" }));
    expect(await screen.findByText("No seasons match.")).toBeInTheDocument();
    noCrash();
  });

  it("QOV-11: hub snapshot with null lists keeps the page alive", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    clusterSocket.reset();
    try {
      setup();
      const ws = FakeSocket.last;
      ws.open();
      ws.recv({ type: "queues", data: snapshotFixture });
      await pageReady();
      ws.recv({
        type: "queues",
        data: { ...snapshotFixture, stages: null, pools: null, flows: null },
      });
      expect(screen.getByRole("heading", { level: 1, name: "Processing queues" })).toBeVisible();
      ws.recv({ type: "snapshot", topic: "jobs", data: { jobs: null, running: null } });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_100);
      });
      expect(screen.getByRole("heading", { level: 1, name: "Processing queues" })).toBeVisible();
      noCrash();
    } finally {
      clusterSocket.reset();
      vi.unstubAllGlobals();
    }
  });

  it("QOV-11: SSE snapshot with queues null keeps the page alive", async () => {
    setup();
    await pageReady();
    act(() =>
      hub.emit("queues", {
        type: "queues.snapshot",
        at: "x",
        payload: { ...snapshotFixture, queues: null },
      })
    );
    expect(screen.getByRole("heading", { level: 1, name: "Processing queues" })).toBeVisible();
    noCrash();
  });

  it("QOV-12: empty but valid snapshot shows empty states", async () => {
    setup({
      "/api/queues": {
        ...snapshotFixture,
        stages: [],
        queues: [],
        pools: [],
        flows: [],
        issues_count: 0,
        paused_all: false,
      },
      "/api/issues": { groups: [] },
      "/api/queues/libraries": { items: [] },
      "/api/workers": { workers: [] },
      "/api/activity": { items: [], next: null },
    });
    expect(await screen.findByText("All caught up")).toBeInTheDocument();
    expect(screen.getByText("Nothing needs attention.")).toBeInTheDocument();
    expect(await screen.findByText("No activity yet.")).toBeInTheDocument();
    expect(screen.getByText("No libraries match.")).toBeInTheDocument();
    expect(await screen.findByText("No workers have reported in.")).toBeInTheDocument();
    expect(document.querySelector("[data-stage]")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Queue work…" }));
    expect(await screen.findByRole("dialog", { name: "Queue work" })).toBeInTheDocument();
  });

  it("QOV-13: render crash shows the route error fallback; Back works", async () => {
    quietCrash();
    const bad = [{ ...failureGroupsFixture[0], label: { not: "a string" } }];
    routeGet(get, table({ "/api/issues": { groups: bad } }));
    renderPage(<QueueOverview />, { path: "/queue" });
    const fallback = await screen.findByRole("alert");
    expect(fallback).toHaveTextContent("This page hit an error and could not be shown.");
    await userEvent.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routeGet(get, table());
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await pageReady()).toBeInTheDocument();
    noCrash();
  });

  it("QOV-14: signed-out user is sent to /login", async () => {
    for (const path of ["/queue", "/queue?host=home"]) {
      const { unmount } = setup({}, { path, user: null });
      expect(await screen.findByText(`login page (from ${path})`)).toBeInTheDocument();
      unmount();
    }
    expect(calls("/api/queues")).toHaveLength(0);
  });

  it("QOV-15: non-admin: run and style controls off; 403 shows a readable alert", async () => {
    post.mockRejectedValue(forbidden());
    setup({}, { user: memberUser });
    await pageReady();
    expect(screen.getByRole("button", { name: "Start run" })).toBeDisabled();
    expect(screen.getByText("Only an admin can start a run.")).toBeInTheDocument();
    expect(screen.getByText("Only an admin can pin a style.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Manage machines" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Pause all" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Forbidden: admin only");
    expect(screen.getByRole("button", { name: "Pause all" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: /^Retry 2/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Forbidden: admin only");
  });

  it.fails("QOV-15: Pause all / Queue work are not offered to a non-admin", async () => {
    setup({}, { user: memberUser });
    await pageReady();
    const pause = screen.queryByRole("button", { name: "Pause all" });
    expect(pause === null || (pause as HTMLButtonElement).disabled).toBe(true);
  });

  it("QOV-16: Pause all then Resume all post and refresh the snapshot", async () => {
    let paused = false;
    setup({ "/api/queues": () => ({ ...snapshotFixture, paused_all: paused }) });
    await pageReady();
    post.mockImplementation(async (url: string) => {
      if (url === "/api/queues/pause-all") paused = true;
      if (url === "/api/queues/resume-all") paused = false;
    });
    const before = calls("/api/queues").length;
    await userEvent.click(screen.getByRole("button", { name: "Pause all" }));
    expect(post).toHaveBeenCalledWith("/api/queues/pause-all");
    expect(await screen.findByRole("button", { name: "Resume all" })).toBeInTheDocument();
    expect(calls("/api/queues").length).toBeGreaterThan(before);
    expect(screen.getAllByText("Paused").length).toBeGreaterThan(0);
    await userEvent.click(screen.getByRole("button", { name: "Resume all" }));
    expect(post).toHaveBeenCalledWith("/api/queues/resume-all");
    expect(await screen.findByRole("button", { name: "Pause all" })).toBeInTheDocument();
    expect(post.mock.calls.filter((c) => c[0] === "/api/queues/pause-all")).toHaveLength(1);
  });

  it("QOV-17: failed mutation shows the API message in an alert", async () => {
    setup();
    await pageReady();
    post.mockRejectedValueOnce(
      new ApiError({
        type: "about:blank",
        title: "Internal Server Error",
        status: 500,
        detail: "db down",
      })
    );
    await userEvent.click(screen.getByRole("button", { name: "Pause all" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Internal Server Error: db down");
    expect(screen.getByRole("button", { name: "Pause all" })).toBeEnabled();
    const issuesBefore = calls("/api/issues").length;
    post.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await userEvent.click(screen.getByRole("button", { name: /^Retry 2/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Failed to fetch");
    expect(calls("/api/issues").length).toBe(issuesBefore);
  });

  it("QOV-18: issue actions: NAS retry label, Apply, settings and workers links", async () => {
    const base = failureGroupsFixture[0];
    const groups = [
      base,
      { ...base, stage: "plex_collection", cause: "plex_review", action: "apply", label: "Ap" },
      { ...base, stage: "link", cause: "no_plex", action: "open_settings", label: "Os" },
      { ...base, stage: "probe", cause: "no_worker", action: "show_workers", label: "Sw" },
    ];
    setup({
      "/api/issues": { groups },
      "/api/queues": {
        ...snapshotFixture,
        health: {
          ...snapshotFixture.health,
          nas: { ...snapshotFixture.health.nas, open_ms_p95: 850 },
        },
      },
    });
    expect(
      await screen.findByRole("button", { name: "Retry 2 (NAS open p95 850 ms)" })
    ).toBeVisible();
    expect(screen.getByRole("link", { name: "Open settings" })).toHaveAttribute(
      "href",
      "/admin/extensions"
    );
    expect(screen.getByRole("link", { name: "Show workers" })).toHaveAttribute(
      "href",
      "/admin/health"
    );
    expect(screen.getAllByRole("link", { name: /Lecture 04/ })[0]).toHaveAttribute(
      "href",
      "/queue/items/video/v-1"
    );
    const before = {
      q: calls("/api/queues").length,
      i: calls("/api/issues").length,
      l: calls("/api/queues/libraries").length,
    };
    post.mockResolvedValue({ enqueued: 3 });
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(post).toHaveBeenCalledWith("/api/queues/plex_collection/apply", { all: true });
    await waitFor(() => {
      expect(calls("/api/queues").length).toBeGreaterThan(before.q);
      expect(calls("/api/issues").length).toBeGreaterThan(before.i);
      expect(calls("/api/queues/libraries").length).toBeGreaterThan(before.l);
    });
  });

  it("QOV-19: Queue work dialog: disabled at 0, no-match alert, Escape closes", async () => {
    setup();
    await pageReady();
    const open = screen.getByRole("button", { name: "Queue work…" });
    await userEvent.click(open);
    const dlg = await screen.findByRole("dialog", { name: "Queue work" });
    expect(within(dlg).getByRole("button", { name: "Queue work (0)" })).toBeDisabled();
    await userEvent.click(within(dlg).getByRole("checkbox", { name: /Islamic lectures/ }));
    // Length (probe) runs on videos only, not on a library
    await userEvent.click(within(dlg).getByRole("checkbox", { name: "Length" }));
    await userEvent.click(within(dlg).getByRole("button", { name: /^Queue work \(/ }));
    expect(await within(dlg).findByRole("alert")).toHaveTextContent(
      "None of the chosen queues work on the chosen items."
    );
    expect(post).not.toHaveBeenCalledWith("/api/queue/retrigger", expect.anything());
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Queue work" })).toBeNull());
  });

  it("QOV-19: Queue work with results null shows a result, not a crash", async () => {
    setup();
    await pageReady();
    post.mockResolvedValue({ results: null });
    await userEvent.click(screen.getByRole("button", { name: "Queue work…" }));
    const dlg = await screen.findByRole("dialog", { name: "Queue work" });
    await userEvent.click(within(dlg).getByRole("checkbox", { name: /Islamic lectures/ }));
    await userEvent.click(within(dlg).getByRole("button", { name: /^Queue work \(/ }));
    expect(
      await within(dlg).findByText("0 queued, 0 added to jobs already waiting, 0 skipped.")
    ).toBeInTheDocument();
    noCrash();
  });

  it("QOV-21: socket drops: SSE takes over, then polling with a note", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    clusterSocket.reset();
    try {
      setup();
      const ws = FakeSocket.last;
      ws.open();
      ws.recv({ type: "queues", data: snapshotFixture });
      await pageReady();
      expect(hub.count("queues")).toBe(0);
      ws.drop();
      await waitFor(() => expect(hub.count("queues")).toBe(1));
      expect(hub.count("jobs")).toBe(1);
      expect(screen.getByRole("link", { name: "Length" })).toBeInTheDocument();
      const before = calls("/api/queues").length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_500);
      });
      expect(
        screen.getByText("Live updates are paused; refreshing every few seconds instead.")
      ).toBeInTheDocument();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(calls("/api/queues").length).toBeGreaterThan(before);
      act(() => hub.emit("queues", events.snapshot(snapshotFixture)));
      await waitFor(() =>
        expect(
          screen.queryByText("Live updates are paused; refreshing every few seconds instead.")
        ).toBeNull()
      );
      const after = calls("/api/queues").length;
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6_000);
      });
      expect(calls("/api/queues").length).toBe(after);
    } finally {
      clusterSocket.reset();
      vi.unstubAllGlobals();
    }
  });

  it("QOV-22: socket reconnects: back on the hub, SSE listeners removed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    clusterSocket.reset();
    try {
      setup();
      const first = FakeSocket.last;
      first.open();
      first.recv({ type: "queues", data: snapshotFixture });
      await pageReady();
      first.drop();
      await waitFor(() => expect(hub.count("queues")).toBe(1));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_500);
      });
      const second = FakeSocket.last;
      expect(second).not.toBe(first);
      second.open();
      expect(second.subscribed().sort()).toEqual(["jobs", "queues", "workers"]);
      await waitFor(() => expect(hub.count("queues")).toBe(0));
      expect(hub.count("jobs")).toBe(0);
      second.recv({ type: "snapshot", topic: "jobs", data: { jobs: [], running: [] } });
      second.recv({ type: "queues", data: snapshotFixture });
      expect(
        screen.queryByText("Live updates are paused; refreshing every few seconds instead.")
      ).toBeNull();
    } finally {
      clusterSocket.reset();
      vi.unstubAllGlobals();
    }
  });

  it("QOV-23: ?host= unknown machine, failing host list, empty host, way back", async () => {
    const a = setup(
      { "/api/queues/running": { host: "node-zz", jobs: [] } },
      { path: "/queue?host=node-zz" }
    );
    expect(await screen.findByRole("heading", { name: /Running on .*node-zz/ })).toBeVisible();
    expect(await screen.findByText(/Nothing is running on .*node-zz.* right now\./)).toBeVisible();
    await userEvent.click(screen.getByRole("link", { name: "Show every machine" }));
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: /Running on .*node-zz/ })).toBeNull()
    );
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("heading", { name: /Running on .*node-zz/ })).toBeVisible();
    a.unmount();

    const b = setup(
      { "/api/queues/running": () => Promise.reject(new Error("500")) },
      { path: "/queue?host=home" }
    );
    expect(await screen.findByRole("heading", { name: /Running on .*this home/ })).toBeVisible();
    expect(
      await screen.findByText(/Nothing is running on .*this home.* right now\./)
    ).toBeVisible();
    b.unmount();

    setup({}, { path: "/queue?host=" });
    await pageReady();
    expect(screen.queryByRole("heading", { name: /^Running on/ })).toBeNull();
  });

  it("QOV-24: links: cards, Reports, Extensions and libraries; Back returns", async () => {
    routeGet(get, table());
    renderPage(<QueueOverview />, {
      path: "/queue",
      extraRoutes: (
        <>
          <Route path="/queue/:stage" element={<p>stage page</p>} />
          <Route path="/queue/reports" element={<p>reports page</p>} />
          <Route path="/queue/items/:kind/:id" element={<p>item page</p>} />
        </>
      ),
    });
    await userEvent.click(await pageReady());
    expect(await screen.findByText("stage page")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    await userEvent.click(await screen.findByRole("link", { name: "Reports" }));
    expect(await screen.findByText("reports page")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("link", { name: "Extensions" })).toHaveAttribute(
      "href",
      "/admin/extensions"
    );
    await userEvent.click(screen.getByRole("link", { name: /Islamic lectures/ }));
    expect(await screen.findByText("item page")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await pageReady()).toBeInTheDocument();
    // not stuck on Loading…: the activity log mounts again with the page and
    // shows Loading… until its own fetch answers
    await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
  });

  it("QOV-25: libraries table: no match, select all, re-queue selected / one", async () => {
    setup();
    await pageReady();
    const search = screen.getByRole("searchbox", { name: "Search libraries" });
    await userEvent.type(search, "zzz");
    expect(screen.getByText("No libraries match.")).toBeInTheDocument();
    await userEvent.clear(search);
    expect(screen.getByRole("button", { name: "Re-queue selected (0)" })).toBeDisabled();
    await userEvent.click(screen.getByRole("checkbox", { name: "Select all libraries" }));
    expect(screen.getByRole("checkbox", { name: "Select Islamic lectures" })).toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Re-queue selected (2)" }));
    const dlg = await screen.findByRole("dialog", { name: "Queue work" });
    expect(within(dlg).getByRole("checkbox", { name: /Islamic lectures/ })).toBeChecked();
    expect(within(dlg).getByRole("checkbox", { name: /قصة وآية/ })).toBeChecked();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await userEvent.click(screen.getByRole("button", { name: /^Re-queue .*Islamic lectures/ }));
    const one = await screen.findByRole("dialog", { name: "Queue work" });
    expect(within(one).getByRole("checkbox", { name: /Islamic lectures/ })).toBeChecked();
    expect(within(one).getByRole("checkbox", { name: /قصة وآية/ })).not.toBeChecked();
  });

  it("QOV-26: activity log: queue filter, Load older failure then success, load error", async () => {
    let older = 0;
    setup({
      "/api/activity": (url: string) => {
        if (url.includes("cursor=c1")) {
          older++;
          if (older === 1) return Promise.reject(new Error("500"));
          return { items: [{ ...logFixture[0], id: 7001, message: "an older line" }], next: null };
        }
        return { items: logFixture, next: "c1" };
      },
    });
    await pageReady();
    const log = screen.getByRole("heading", { name: "Activity" }).closest("section")!;
    await userEvent.selectOptions(within(log).getByRole("combobox", { name: "Queue" }), "probe");
    await waitFor(() =>
      expect(
        get.mock.calls.some(
          (c) => String(c[0]).includes("/api/activity") && String(c[0]).includes("stage=probe")
        )
      ).toBe(true)
    );
    const olderBtn = await within(log).findByRole("button", { name: "Load older" });
    await userEvent.click(olderBtn);
    expect(await within(log).findByRole("button", { name: "Load older" })).toBeEnabled();
    await userEvent.click(within(log).getByRole("button", { name: "Load older" }));
    expect(await within(log).findByText("an older line")).toBeInTheDocument();
    await waitFor(() =>
      expect(within(log).queryByRole("button", { name: "Load older" })).toBeNull()
    );
  });

  it("QOV-26: the first activity request failing shows the load error", async () => {
    setup({ "/api/activity": () => Promise.reject(new Error("500")) });
    await pageReady();
    expect(await screen.findByText("The activity log could not be loaded.")).toBeInTheDocument();
  });
});
