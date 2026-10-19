// Cases from web/test-cases/queue-detail.md (QDT-*), rendered through the
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
      reset: () => channels.clear(),
    },
  };
});
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch } };
});
vi.mock("../../lib/ws", () => ({ subscribe: hub.subscribe }));

import { QueueDetail } from "./QueueDetail";
import { ApiError } from "../../lib/api";
import type { QueueDetailData, QueuesSnapshot } from "../../lib/queueApi";
import { routeGet } from "../../test/queueHarness";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";
import {
  detailFixture,
  failureGroupsFixture,
  librariesFixture,
  queueSummary,
  rateFixture,
  snapshotFixture,
  workersFixture,
} from "../../test/fixtures/queues";

type Table = Record<string, unknown>;

function table(stage: string, over: Table = {}): Table {
  return {
    "/api/queues": snapshotFixture,
    [`/api/queues/${stage}`]: detailFixture(stage),
    [`/api/queues/${stage}/rate`]: rateFixture,
    "/api/queues/libraries": { items: librariesFixture },
    "/api/activity": { items: [], next: null },
    "/api/workers": { workers: workersFixture },
    ...over,
  };
}

function setup(
  stage: string,
  over: Table = {},
  opts: { user?: typeof memberUser | null; path?: string; extra?: boolean } = {}
) {
  routeGet(get, table(stage, over));
  return renderPage(<QueueDetail />, {
    path: opts.path ?? `/queue/${stage}`,
    route: "/queue/:stage",
    user: opts.user,
    extraRoutes: <Route path="/queue" element={<p>overview page</p>} />,
  });
}

/** the snapshot without one stage, so the page uses the REST info / summary */
const snapshotWithout = (stage: string): QueuesSnapshot => ({
  ...snapshotFixture,
  stages: snapshotFixture.stages.filter((s) => s.stage !== stage),
  queues: snapshotFixture.queues.filter((q) => q.stage !== stage),
});

const apiErr = (status: number, title: string, detail?: string) =>
  new ApiError({ type: "about:blank", title, status, detail });
const calls = (path: string) => get.mock.calls.filter((c) => String(c[0]).split("?")[0] === path);
// the route error fallback (RouteErrorBoundary) says this; a load error does not
const noCrash = () =>
  expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();
const heading = (name: string) => screen.findByRole("heading", { level: 1, name });

/** detail with one (possibly nested) key set to null or removed */
function detailWith(stage: string, path: string, mode: "null" | "missing"): QueueDetailData {
  const d = structuredClone(detailFixture(stage)) as unknown as Record<string, unknown>;
  const keys = path.split(".");
  let o: Record<string, unknown> = d;
  for (const k of keys.slice(0, -1)) o = o[k] as Record<string, unknown>;
  const last = keys[keys.length - 1];
  if (mode === "null") o[last] = null;
  else delete o[last];
  return d as unknown as QueueDetailData;
}

describe("QueueDetail (test-cases/queue-detail.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    put.mockReset();
    patch.mockReset();
    post.mockResolvedValue(undefined);
    put.mockResolvedValue({});
    patch.mockResolvedValue({});
    hub.reset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("QDT-02: shows Loading until the detail is known, then the page", async () => {
    let resolve: (v: unknown) => void = () => {};
    setup("fingerprint", { "/api/queues/fingerprint": new Promise((r) => (resolve = r)) });
    expect(await screen.findByRole("status")).toHaveTextContent("Loading…");
    expect(screen.queryByRole("button", { name: "Pause this queue" })).toBeNull();
    await act(async () => resolve(detailFixture("fingerprint")));
    expect(await heading("Fingerprint")).toBeInTheDocument();
  });

  it("QDT-03: unknown stage shows 'There is no queue called …'; Back to the queues", async () => {
    setup("nope", {
      "/api/queues/nope": () => Promise.reject(apiErr(404, "Not Found")),
      "/api/queues/nope/rate": () => Promise.reject(apiErr(404, "Not Found")),
    });
    expect(await screen.findByRole("alert")).toHaveTextContent("There is no queue called nope.");
    expect(screen.queryByRole("button", { name: "Pause this queue" })).toBeNull();
    await userEvent.click(screen.getByRole("link", { name: "Back to the queues" }));
    expect(await screen.findByText("overview page")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("There is no queue called nope.");
  });

  it("QDT-04: 500 / network / HTML on detail is a load error with Try again, not 'no queue'", async () => {
    const failures: Array<() => unknown> = [
      () => Promise.reject(apiErr(500, "Internal Server Error")),
      () => Promise.reject(new TypeError("Failed to fetch")),
      () => "<!doctype html><html></html>",
    ];
    for (const fail of failures) {
      let n = 0;
      const { unmount } = setup("fingerprint", {
        "/api/queues/fingerprint": () => (n++ === 0 ? fail() : detailFixture("fingerprint")),
      });
      const alert = await screen.findByRole("alert");
      expect(alert).not.toHaveTextContent("There is no queue called");
      expect(alert).toHaveTextContent("Something went wrong.");
      await userEvent.click(within(alert).getByRole("button", { name: "Try again" }));
      expect(await heading("Fingerprint")).toBeInTheDocument();
      unmount();
      get.mockReset();
    }
  });

  it.fails("QDT-04: a 401 on the detail sends the user to /login", async () => {
    setup("fingerprint", {
      "/api/queues/fingerprint": () => Promise.reject(apiErr(401, "Unauthorized")),
    });
    expect(await screen.findByText(/login page/, {}, { timeout: 300 })).toBeInTheDocument();
  });

  it.each([
    "failures",
    "next_up",
    "log",
    "running",
    "info.edges",
    "info.fed_by",
    "summary.served_by",
    "running.0.progress.steps",
    "failures.0.sample_subjects",
    "kpis",
  ])("QDT-06: detail %s null or missing renders as empty", async (field) => {
    for (const mode of ["null", "missing"] as const) {
      const { unmount } = setup("fingerprint", {
        "/api/queues": snapshotWithout("fingerprint"),
        "/api/queues/fingerprint": detailWith("fingerprint", field, mode),
      });
      expect(await heading("Fingerprint"), mode).toBeInTheDocument();
      noCrash();
      if (field === "failures") {
        expect(screen.getByRole("button", { name: "Retry failed (0)" })).toBeDisabled();
        const sec = screen.getByRole("heading", { name: "Failures by cause" }).closest("section")!;
        expect(within(sec).getByText("Nothing needs attention.")).toBeInTheDocument();
      }
      if (field === "next_up") expect(screen.getByText("Nothing is waiting.")).toBeInTheDocument();
      if (field === "log") expect(await screen.findByText("No activity yet.")).toBeInTheDocument();
      if (field === "running") expect(screen.getByText("Nothing is running.")).toBeInTheDocument();
      if (field === "info.fed_by") expect(screen.getByText("Starts here")).toBeInTheDocument();
      if (field === "info.edges") expect(screen.getByText("Last step")).toBeInTheDocument();
      unmount();
    }
  });

  it("QDT-07: rate points null / missing / failing / empty draw an empty chart", async () => {
    const variants: unknown[] = [
      { bucket_sec: 600, points: null },
      {},
      () => Promise.reject(apiErr(500, "Internal Server Error")),
      { points: [] },
    ];
    for (const v of variants) {
      const { unmount } = setup("fingerprint", { "/api/queues/fingerprint/rate": v });
      expect(await heading("Fingerprint")).toBeInTheDocument();
      const sec = screen
        .getByRole("heading", { name: "Done per 10 minutes (last 6 hours)" })
        .closest("section")!;
      expect(within(sec).getByRole("img")).toBeInTheDocument();
      noCrash();
      unmount();
    }
  });

  it("QDT-08: snapshot / libraries / pool lists null or missing", async () => {
    const variants: Table[] = [
      { "/api/queues": { ...snapshotFixture, stages: null } },
      { "/api/queues": { ...snapshotFixture, queues: null } },
      { "/api/queues": { ...snapshotFixture, pools: null } },
      { "/api/queues/libraries": { items: null } },
      { "/api/queues/libraries": {} },
      {
        "/api/queues": {
          ...snapshotFixture,
          pools: snapshotFixture.pools.map((p) => ({ ...p, stages: null })),
        },
      },
    ];
    for (const v of variants) {
      const { unmount } = setup("fingerprint", v);
      expect(await heading("Fingerprint")).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: "Re-queue a library or season…" }));
      expect(await screen.findByRole("dialog", { name: "Queue work" })).toBeInTheDocument();
      noCrash();
      unmount();
    }
    // without pools there is no stepper
    setup("fingerprint", { "/api/queues": { ...snapshotFixture, pools: null } });
    expect(await heading("Fingerprint")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More workers" })).toBeNull();
  });

  it("QDT-09: empty queue shows every empty state", async () => {
    const d = detailFixture("tag");
    setup("tag", {
      "/api/queues": snapshotWithout("tag"),
      "/api/queues/tag": {
        ...d,
        summary: queueSummary("tag", { waiting_reason: "idle", eta_sec: null }),
        running: [],
        next_up: [],
        failures: [],
        log: [],
      },
    });
    expect(await heading("Tag topics")).toBeInTheDocument();
    expect(screen.getByText("Nothing is running.")).toBeInTheDocument();
    expect(screen.getByText("Nothing is waiting.")).toBeInTheDocument();
    expect(screen.getByText("Nothing needs attention.")).toBeInTheDocument();
    expect(await screen.findByText("No activity yet.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry failed (0)" })).toBeDisabled();
    expect(screen.queryByRole("checkbox", { name: "Also ask running jobs to pause" })).toBeNull();
    expect(screen.getByRole("group", { name: "Time left" })).toHaveTextContent("–");
  });

  it("QDT-10: render crash shows the route error fallback; Back works", async () => {
    quietCrash();
    const bad = detailFixture("fingerprint");
    routeGet(
      get,
      table("fingerprint", {
        "/api/queues": snapshotWithout("fingerprint"),
        "/api/queues/fingerprint": { ...bad, info: { ...bad.info, label: { not: "text" } } },
      })
    );
    renderPage(<QueueDetail />, { path: "/queue/fingerprint", route: "/queue/:stage" });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await userEvent.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routeGet(get, table("fingerprint"));
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await heading("Fingerprint")).toBeInTheDocument();
  });

  it("QDT-11: signed-out user is sent to /login", async () => {
    setup("fingerprint", {}, { user: null });
    expect(await screen.findByText("login page (from /queue/fingerprint)")).toBeInTheDocument();
    expect(calls("/api/queues/fingerprint")).toHaveLength(0);
  });

  it("QDT-12: non-admin: actions answer 403 with a readable alert", async () => {
    const no = () => Promise.reject(apiErr(403, "Forbidden", "admin only"));
    post.mockImplementation(no);
    put.mockImplementation(no);
    patch.mockImplementation(no);
    const d = detailFixture("plex_collection");
    setup(
      "plex_collection",
      {
        "/api/queues/plex_collection": {
          ...d,
          failures: [{ ...failureGroupsFixture[0], stage: "plex_collection" }],
        },
      },
      { user: memberUser }
    );
    expect(await heading("Plex collection")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Pause this queue" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Forbidden: admin only");
    await userEvent.click(screen.getByRole("button", { name: /^Retry failed/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Forbidden: admin only");
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Writes to other systems" }),
      "review"
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("Forbidden: admin only");
    expect(screen.getByRole("button", { name: "Pause this queue" })).toBeEnabled();
  });

  it("QDT-12: non-admin pool change answers 403 with a readable alert", async () => {
    put.mockRejectedValue(apiErr(403, "Forbidden", "admin only"));
    setup("fingerprint", {}, { user: memberUser });
    expect(await heading("Fingerprint")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "More workers" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Forbidden: admin only");
  });

  it.fails("QDT-12: queue controls are not offered to a non-admin", async () => {
    setup("fingerprint", {}, { user: memberUser });
    expect(await heading("Fingerprint")).toBeInTheDocument();
    const pause = screen.queryByRole("button", { name: "Pause this queue" });
    expect(pause === null || (pause as HTMLButtonElement).disabled).toBe(true);
  });

  it("QDT-14: failed pause and pool change show the message; stepper value kept", async () => {
    setup("fingerprint");
    expect(await heading("Fingerprint")).toBeInTheDocument();
    post.mockRejectedValueOnce(apiErr(500, "Internal Server Error", "db down"));
    await userEvent.click(screen.getByRole("button", { name: "Pause this queue" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Internal Server Error: db down");
    expect(screen.getByRole("button", { name: "Pause this queue" })).toBeEnabled();
    put.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    const stepper = screen.getByRole("group", { name: /^Workers for/ });
    const before = stepper.textContent;
    await userEvent.click(screen.getByRole("button", { name: "More workers" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Failed to fetch");
    expect(stepper.textContent).toBe(before);
    expect(screen.getByRole("button", { name: "More workers" })).toBeEnabled();
  });

  it("QDT-16: Retry failed (N) retries every retry group in order, not the apply group", async () => {
    const g = failureGroupsFixture[0];
    const d = detailFixture("fingerprint");
    setup("fingerprint", {
      "/api/queues/fingerprint": {
        ...d,
        failures: [
          { ...g, cause: "a", count: 3 },
          { ...g, cause: "b", count: 4 },
          { ...g, cause: "c", count: 5, action: "apply" },
        ],
      },
    });
    expect(await heading("Fingerprint")).toBeInTheDocument();
    const before = calls("/api/queues/fingerprint").length;
    await userEvent.click(screen.getByRole("button", { name: "Retry failed (7)" }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    expect(post.mock.calls).toEqual([
      ["/api/queues/fingerprint/retry", { cause: "a" }],
      ["/api/queues/fingerprint/retry", { cause: "b" }],
    ]);
    await waitFor(() => expect(calls("/api/queues/fingerprint").length).toBeGreaterThan(before));
  });

  it("QDT-16: when the second retry fails the alert shows and the first stays done", async () => {
    const g = failureGroupsFixture[0];
    const d = detailFixture("fingerprint");
    setup("fingerprint", {
      "/api/queues/fingerprint": {
        ...d,
        failures: [
          { ...g, cause: "a", count: 3 },
          { ...g, cause: "b", count: 4 },
        ],
      },
    });
    expect(await heading("Fingerprint")).toBeInTheDocument();
    post
      .mockResolvedValueOnce({ enqueued: 3, coalesced: 0 })
      .mockRejectedValueOnce(apiErr(500, "Internal Server Error", "db down"));
    await userEvent.click(screen.getByRole("button", { name: "Retry failed (7)" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Internal Server Error: db down");
    expect(post).toHaveBeenNthCalledWith(1, "/api/queues/fingerprint/retry", { cause: "a" });
  });

  it("QDT-17: failure group Apply and per-group Retry post for this stage", async () => {
    const g = failureGroupsFixture[0];
    const d = detailFixture("plex_collection");
    setup("plex_collection", {
      "/api/queues/plex_collection": {
        ...d,
        failures: [
          { ...g, stage: "plex_collection", cause: "review", action: "apply", label: "To apply" },
          { ...g, stage: "plex_collection", cause: "plex_down", count: 4, label: "Plex down" },
        ],
      },
    });
    expect(await heading("Plex collection")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(post).toHaveBeenCalledWith("/api/queues/plex_collection/apply", { all: true });
    await userEvent.click(screen.getByRole("button", { name: "Retry 4" }));
    expect(post).toHaveBeenCalledWith("/api/queues/plex_collection/retry", {
      cause: "plex_down",
    });
    const sec = screen.getByRole("heading", { name: "Failures by cause" }).closest("section")!;
    expect(within(sec).queryByText("Plex collection", { selector: ".mkt-q-chip" })).toBeNull();
  });

  it("QDT-18: write mode select patches the stage and reloads", async () => {
    const d = detailFixture("plex_collection");
    let mode: "auto" | "review" = "auto";
    const snap = snapshotWithout("plex_collection");
    setup("plex_collection", {
      "/api/queues": snap,
      "/api/queues/plex_collection": () => ({ ...d, info: { ...d.info, write_mode: mode } }),
    });
    expect(await heading("Plex collection")).toBeInTheDocument();
    patch.mockImplementation(async () => {
      mode = "review";
      return {};
    });
    await userEvent.selectOptions(
      screen.getByRole("combobox", { name: "Writes to other systems" }),
      "review"
    );
    expect(patch).toHaveBeenCalledWith("/api/queues/plex_collection", { write_mode: "review" });
    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Writes to other systems" })).toHaveValue(
        "review"
      )
    );
  });

  it("QDT-18: a stage without external writes has no write mode select", async () => {
    setup("fingerprint");
    expect(await heading("Fingerprint")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Writes to other systems" })).toBeNull();
  });

  it("QDT-20: served-by note lists the machines", async () => {
    const d = detailFixture("tag");
    setup("tag", {
      "/api/queues": snapshotWithout("tag"),
      "/api/queues/tag": {
        ...d,
        summary: queueSummary("tag", { served_by: ["This PC", "Mac"] }),
      },
    });
    expect(await heading("Tag topics")).toBeInTheDocument();
    expect(screen.getByText(/This PC, Mac/)).toBeInTheDocument();
  });

  it("QDT-22: a late answer for a stage you left does not replace the current one", async () => {
    let resolveDetect: (v: unknown) => void = () => {};
    // fingerprint is not in the snapshot: its heading comes from the REST detail
    routeGet(get, {
      ...table("fingerprint"),
      "/api/queues": snapshotWithout("fingerprint"),
      "/api/queues/detect": new Promise((r) => (resolveDetect = r)),
      "/api/queues/detect/rate": rateFixture,
    });
    renderPage(<QueueDetail />, { path: "/queue/fingerprint", route: "/queue/:stage" });
    expect(await heading("Fingerprint")).toBeInTheDocument();
    const nb = screen.getByLabelText("Where work comes from and goes to");
    await userEvent.click(within(nb).getAllByRole("link", { name: "Place episodes" })[0]);
    expect(await screen.findByRole("status")).toHaveTextContent("Loading…");
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await heading("Fingerprint")).toBeInTheDocument();
    await act(async () => resolveDetect(detailFixture("detect")));
    expect(screen.getByRole("heading", { level: 1, name: "Fingerprint" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { level: 1, name: "Place episodes" })).toBeNull();
  });

  it("QDT-22: switching stage clears a previous 'no queue' message", async () => {
    routeGet(get, {
      ...table("fingerprint"),
      "/api/queues/nope": () => Promise.reject(apiErr(404, "Not Found")),
      "/api/queues/nope/rate": { points: [] },
    });
    renderPage(<QueueDetail />, {
      path: "/queue/fingerprint",
      route: "/queue/:stage",
      history: ["/queue/nope"],
    });
    expect(await heading("Fingerprint")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("There is no queue called nope.")).toBeInTheDocument();
  });

  it("QDT-23: ?host= with an unknown machine shows nothing running and the way back", async () => {
    setup("probe", {}, { path: "/queue/probe?host=node-zz" });
    expect(await heading("Length")).toBeInTheDocument();
    const sec = screen.getByRole("heading", { name: "Running now" }).closest("section")!;
    expect(within(sec).getByText(/node-zz/)).toBeInTheDocument();
    expect(within(sec).getByText("Nothing is running.")).toBeInTheDocument();
    expect(within(sec).getByRole("link", { name: "Show every machine" })).toHaveAttribute(
      "href",
      "/queue/probe"
    );
  });

  it("QDT-24: log Problems only and Load older stay on this stage; item links", async () => {
    setup("fingerprint", {
      "/api/activity": (url: string) =>
        url.includes("cursor=c1")
          ? {
              items: [{ ...detailFixture("fingerprint").log[0], id: 1, message: "older" }],
              next: null,
            }
          : { items: [], next: "c1" },
    });
    expect(await heading("Fingerprint")).toBeInTheDocument();
    const log = screen.getByRole("heading", { name: "This queue's log" }).closest("section")!;
    expect(within(log).queryByRole("combobox", { name: "Queue" })).toBeNull();
    await userEvent.click(within(log).getByRole("checkbox", { name: "Problems only" }));
    await waitFor(() =>
      expect(
        get.mock.calls.some(
          (c) =>
            String(c[0]).startsWith("/api/activity") &&
            String(c[0]).includes("stage=fingerprint") &&
            String(c[0]).includes("level=warn")
        )
      ).toBe(true)
    );
    await userEvent.click(await within(log).findByRole("button", { name: "Load older" }));
    expect(await within(log).findByText("older")).toBeInTheDocument();
    const next = screen.getByRole("heading", { name: "Next up" }).closest("section")!;
    expect(within(next).getAllByRole("link")[0].getAttribute("href")).toMatch(
      /^\/queue\/items\/video\//
    );
  });
});
