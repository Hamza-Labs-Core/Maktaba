import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

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
