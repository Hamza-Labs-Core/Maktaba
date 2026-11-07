import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { deferred, type Deferred } from "../../test/deferred";

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
import { SNAPSHOT_POLL_MS, SNAPSHOT_SILENCE_MS } from "../../lib/useQueueStream";
import { renderAt, routeGet } from "../../test/queueHarness";
import {
  detailFixture,
  events,
  jobRef,
  librariesFixture,
  rateFixture,
  snapshotFixture,
} from "../../test/fixtures/queues";

function setup(stage: string, snapshot = snapshotFixture) {
  routeGet(get, {
    "/api/queues": snapshot,
    [`/api/queues/${stage}`]: detailFixture(stage),
    [`/api/queues/${stage}/rate`]: rateFixture,
    "/api/queues/libraries": { items: librariesFixture },
    "/api/activity": { items: [], next: null },
  });
  post.mockResolvedValue(undefined);
  put.mockResolvedValue({});
  return renderAt(<QueueDetail />, `/queue/${stage}`, "/queue/:stage");
}

describe("QueueDetail", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
    put.mockReset();
    hub.reset();
  });

  it("renders breadcrumb, description, KPIs, neighbours, chart, running and failures", async () => {
    setup("fingerprint");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Fingerprint" })
    ).toBeInTheDocument();
    const crumbs = screen.getByRole("navigation", { name: "Breadcrumb" });
    expect(within(crumbs).getByRole("link", { name: "Processing queues" })).toHaveAttribute(
      "href",
      "/queue"
    );
    expect(screen.getByText(/Reads a short audio and picture fingerprint/)).toBeInTheDocument();
    // KPI tiles
    expect(screen.getByText("120").closest("div")).toHaveTextContent("Waiting");
    expect(screen.getByRole("group", { name: "Running" })).toHaveTextContent("1of 2"); // running of NAS pool
    expect(screen.getByText("Gave up")).toBeInTheDocument();
    expect(screen.getByText("Median per job")).toBeInTheDocument();
    // fed by -> this -> hands on to
    const nb = screen.getByLabelText("Where work comes from and goes to");
    expect(within(nb).getAllByRole("link", { name: "Place episodes" })[0]).toHaveAttribute(
      "href",
      "/queue/detect"
    );
    // chart has a text alternative
    expect(screen.getByRole("img", { name: /Jobs finished per 10 minutes/ })).toBeInTheDocument();
    // running job with sub-steps
    expect(screen.getByText("tail window")).toHaveAttribute("aria-current", "step");
    // failures grouped by cause with retry
    expect(screen.getByRole("button", { name: /Retry 2/ })).toBeInTheDocument();
    // next up
    expect(screen.getByText(/Lecture 05/)).toBeInTheDocument();
    // pool is shared: states so
    expect(screen.getByText(/shared with/)).toBeInTheDocument();
  });

  it("pauses the queue, optionally interrupting running jobs", async () => {
    setup("fingerprint");
    await userEvent.click(await screen.findByLabelText("Also ask running jobs to pause"));
    await userEvent.click(screen.getByRole("button", { name: "Pause this queue" }));
    expect(post).toHaveBeenCalledWith("/api/queues/fingerprint/pause?interrupt=true");
  });

  it("offers Resume for a paused queue", async () => {
    const snap = {
      ...snapshotFixture,
      stages: snapshotFixture.stages.map((s) => (s.stage === "link" ? { ...s, paused: true } : s)),
    };
    setup("link", snap);
    await userEvent.click(await screen.findByRole("button", { name: "Resume this queue" }));
    expect(post).toHaveBeenCalledWith("/api/queues/link/resume");
  });

  it("shows a built-in plugin's stage and flips where it runs (Epic 54)", async () => {
    const snap = {
      ...snapshotFixture,
      stages: snapshotFixture.stages.map((s) =>
        s.stage === "link" ? { ...s, plugin: "tv.maktaba.core-media", impl: "plugin" as const } : s
      ),
    };
    routeGet(get, {
      "/api/queues": snap,
      "/api/queues/link": detailFixture("link"),
      "/api/queues/link/rate": rateFixture,
      "/api/queues/libraries": { items: librariesFixture },
      "/api/activity": { items: [], next: null },
    });
    put.mockResolvedValue({ stage: "link", impl: "inproc", plugin: "tv.maktaba.core-media", paused: 0 });
    renderAt(<QueueDetail />, "/queue/link", "/queue/:stage");
    const box = await screen.findByTestId("queue-builtin-plugin");
    expect(within(box).getByText("Built-in plugin")).toBeInTheDocument();
    const menu = within(box).getByRole("combobox", { name: "Runs in" });
    expect(menu).toHaveValue("plugin");
    await userEvent.selectOptions(menu, "inproc");
    await waitFor(() => expect(put).toHaveBeenCalledWith("/api/queues/link/impl", { impl: "inproc" }));
  });

  it("shows no built-in plugin box for the core's own stages", async () => {
    setup("link");
    await screen.findByRole("heading", { level: 1 });
    expect(screen.queryByTestId("queue-builtin-plugin")).toBeNull();
  });

  it("says who paused the queue and when", async () => {
    const snap = {
      ...snapshotFixture,
      stages: snapshotFixture.stages.map((s) =>
        s.stage === "link"
          ? {
              ...s,
              paused: true,
              paused_at: new Date(Date.now() - 2 * 3600_000).toISOString(),
              paused_by: "user:11111111-1111-1111-1111-111111111111",
              paused_by_name: "mahmoud",
            }
          : s
      ),
    };
    setup("link", snap);
    const banner = await screen.findByTestId("queue-paused-banner");
    const text = (banner.textContent ?? "").replace(/[⁨⁩]/g, "");
    expect(text).toContain("Paused by mahmoud");
    expect(text).toContain("ago");
    expect(text).toContain("Nothing in this queue starts until you resume it.");
  });

  it("says a queue paused with no owner on record was paused by no one", async () => {
    const snap = {
      ...snapshotFixture,
      stages: snapshotFixture.stages.map((s) =>
        s.stage === "link" ? { ...s, paused: true, paused_at: null, paused_by: null } : s
      ),
    };
    setup("link", snap);
    expect((await screen.findByTestId("queue-paused-banner")).textContent).toBe(
      "Paused by no one on record. Nothing in this queue starts until you resume it."
    );
  });

  it("steps the shared pool size and respects the bounds", async () => {
    setup("fingerprint");
    await userEvent.click(await screen.findByRole("button", { name: "More workers" }));
    expect(put).toHaveBeenCalledWith("/api/pools/nas_read", { size: 3 });
    await userEvent.click(screen.getByRole("button", { name: "Fewer workers" }));
    expect(put).toHaveBeenCalledWith("/api/pools/nas_read", { size: 1 });
    await userEvent.click(screen.getByRole("button", { name: "Auto" }));
    expect(put).toHaveBeenCalledWith("/api/pools/nas_read", { size: null });
  });

  it("disables Fewer workers at 0 and More workers at 64", async () => {
    const mk = (size: number) => ({
      ...snapshotFixture,
      pools: snapshotFixture.pools.map((p) =>
        p.pool === "gpu" ? { ...p, size, effective_size: size } : p
      ),
    });
    const { unmount } = setup("transcribe", mk(0));
    expect(await screen.findByRole("button", { name: "Fewer workers" })).toBeDisabled();
    unmount();
    get.mockReset();
    setup("transcribe", mk(64));
    expect(await screen.findByRole("button", { name: "More workers" })).toBeDisabled();
  });

  it("retries failed groups by cause", async () => {
    setup("link");
    await userEvent.click(await screen.findByRole("button", { name: /Retry failed \(7\)/ }));
    expect(post).toHaveBeenCalledWith("/api/queues/link/retry", { cause: "youtube_bot_check" });
  });

  it("tracks running jobs live from the job stream", async () => {
    setup("probe");
    await screen.findByRole("heading", { level: 1, name: "Length" });
    expect(screen.getAllByText(/حلقة 12/).length).toBeGreaterThan(0);
    const fresh = jobRef({ id: 7777, stage: "probe", subject_label: "Brand new episode" });
    act(() => hub.emit("jobs", events.jobStarted(fresh)));
    expect(await screen.findByText(/Brand new episode/, {}, { timeout: 2500 })).toBeInTheDocument();
    act(() => hub.emit("jobs", events.jobFinished(fresh)));
    await waitFor(() => expect(screen.queryByText(/Brand new episode/)).toBeNull(), {
      timeout: 2500,
    });
  });

  it("explains when no live worker serves the queue", async () => {
    const snap = {
      ...snapshotFixture,
      queues: snapshotFixture.queues.map((q) =>
        q.stage === "transcribe"
          ? { ...q, no_worker: true, counts: { ...q.counts, running: 0 } }
          : q
      ),
    };
    setup("transcribe", snap);
    expect(
      await screen.findAllByText("Waiting for a worker that serves Transcribe")
    ).not.toHaveLength(0);
  });

  it("opens the re-queue dialog prefilled with this queue", async () => {
    setup("detect");
    await userEvent.click(
      await screen.findByRole("button", { name: /Re-queue a library or season/ })
    );
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("checkbox", { name: "Place episodes" })).toBeChecked();
  });
});

// A read still on its way when the queue is changed here has the old state: once
// the home has the change, its answer must not undo it on screen.
describe("QueueDetail, reads and changes in order", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset().mockResolvedValue(undefined);
    patch.mockReset().mockResolvedValue({});
    hub.reset();
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const user = () => userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
  const calls = (path: string) => get.mock.calls.filter((c) => String(c[0]).split("?")[0] === path);
  /** The answers in turn, then `rest` for every read after them. */
  const inTurn =
    (rest: unknown, ...answers: unknown[]) =>
    () =>
      answers.length ? answers.shift() : rest;
  function routes(stage: string, queues: () => unknown, detail: () => unknown) {
    routeGet(get, {
      "/api/queues": queues,
      [`/api/queues/${stage}`]: detail,
      [`/api/queues/${stage}/rate`]: rateFixture,
      "/api/queues/libraries": { items: librariesFixture },
      "/api/activity": { items: [], next: null },
    });
    return renderAt(<QueueDetail />, `/queue/${stage}`, "/queue/:stage");
  }
  const withStage = (stage: string, over: Record<string, unknown>) => ({
    ...snapshotFixture,
    stages: snapshotFixture.stages.map((s) => (s.stage === stage ? { ...s, ...over } : s)),
  });
  /** The SSE stream is silent: after its 10 s the page polls /api/queues every 5 s. */
  const untilThePoll = () =>
    act(async () => {
      await vi.advanceTimersByTimeAsync(SNAPSHOT_SILENCE_MS + SNAPSHOT_POLL_MS);
    });
  const answer = async (d: Deferred<unknown>, v: unknown) => {
    d.resolve(v);
    await act(async () => {
      await d.promise;
    });
  };

  it.each([
    ["Retry", /^Retry 2/, "retry" as const, "/api/queues/fingerprint/retry"],
    ["Apply", /^Apply$/, "apply" as const, "/api/queues/fingerprint/apply"],
  ])(
    "a reload after a finished job that began before %s does not bring the group back",
    async (_what, button, action, url) => {
      const base = detailFixture("fingerprint");
      const before = { ...base, failures: base.failures.map((g) => ({ ...g, action })) };
      const older = deferred<unknown>();
      routes(
        "fingerprint",
        () => snapshotFixture,
        inTurn({ ...base, failures: [] }, before, older.promise)
      );
      const u = user();
      await screen.findByRole("button", { name: button });
      // a job finishes: the page reads the queue again, at most once per 10 s
      act(() => hub.emit("jobs", events.jobFinished(jobRef({ id: 4242, stage: "fingerprint" }))));
      await act(async () => {
        await vi.advanceTimersByTimeAsync(11_000);
      });
      expect(calls("/api/queues/fingerprint")).toHaveLength(2);

      await u.click(screen.getByRole("button", { name: button }));
      expect(post).toHaveBeenCalledWith(url, expect.anything());
      expect(await screen.findByText("Nothing needs attention.")).toBeInTheDocument();

      await answer(older, before);
      expect(screen.getByText("Nothing needs attention.")).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: button })).toBeNull();
    }
  );

  it("a snapshot poll that began before Pause does not show the queue running again", async () => {
    const older = deferred<unknown>();
    const paused = withStage("fingerprint", { paused: true });
    routes("fingerprint", inTurn(paused, snapshotFixture, older.promise), () =>
      detailFixture("fingerprint")
    );
    const u = user();
    await screen.findByRole("button", { name: "Pause this queue" });
    await untilThePoll();
    expect(calls("/api/queues")).toHaveLength(2);

    await u.click(screen.getByRole("button", { name: "Pause this queue" }));
    expect(post).toHaveBeenCalledWith("/api/queues/fingerprint/pause");
    // the home's stream says so
    act(() => hub.emit("queues", { type: "queues.snapshot", payload: paused }));
    expect(await screen.findByRole("button", { name: "Resume this queue" })).toBeInTheDocument();

    await answer(older, snapshotFixture);
    expect(screen.getByRole("button", { name: "Resume this queue" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pause this queue" })).toBeNull();
  });

  it("a snapshot poll that began before Resume does not show the queue paused again", async () => {
    const older = deferred<unknown>();
    const paused = withStage("link", { paused: true });
    routes("link", inTurn(snapshotFixture, paused, older.promise), () => detailFixture("link"));
    const u = user();
    await screen.findByRole("button", { name: "Resume this queue" });
    await untilThePoll();
    expect(calls("/api/queues")).toHaveLength(2);

    await u.click(screen.getByRole("button", { name: "Resume this queue" }));
    expect(post).toHaveBeenCalledWith("/api/queues/link/resume");
    act(() => hub.emit("queues", { type: "queues.snapshot", payload: snapshotFixture }));
    expect(await screen.findByRole("button", { name: "Pause this queue" })).toBeInTheDocument();

    await answer(older, paused);
    expect(screen.getByRole("button", { name: "Pause this queue" })).toBeInTheDocument();
  });

  it("a snapshot poll that began before a write-mode change does not set it back", async () => {
    const older = deferred<unknown>();
    const review = withStage("plex_collection", { write_mode: "review" });
    routes("plex_collection", inTurn(review, snapshotFixture, older.promise), () =>
      detailFixture("plex_collection")
    );
    const u = user();
    const mode = await screen.findByRole("combobox", { name: "Writes to other systems" });
    expect(mode).toHaveValue("auto");
    await untilThePoll();
    expect(calls("/api/queues")).toHaveLength(2);

    await u.selectOptions(mode, "review");
    expect(patch).toHaveBeenCalledWith("/api/queues/plex_collection", { write_mode: "review" });
    act(() => hub.emit("queues", { type: "queues.snapshot", payload: review }));
    await waitFor(() => expect(mode).toHaveValue("review"));

    await answer(older, snapshotFixture);
    expect(screen.getByRole("combobox", { name: "Writes to other systems" })).toHaveValue("review");
  });
});
