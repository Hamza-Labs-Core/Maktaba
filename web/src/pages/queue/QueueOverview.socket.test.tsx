// The Processing page on the cluster hub's WebSocket (design-agent.md §5):
// queues, jobs and workers come from the socket; the SSE stream takes over
// when the socket cannot connect.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { deferred } from "../../test/deferred";

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
      count: (ch: string) => channels.get(ch)?.size ?? 0,
      reset: () => channels.clear(),
    },
  };
});
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("../../lib/ws", () => ({ subscribe: hub.subscribe }));

import { QueueOverview } from "./QueueOverview";
import { clusterSocket } from "../../lib/clusterSocket";
import { FakeSocket } from "../../test/fakeSocket";
import { routeGet, renderAt } from "../../test/queueHarness";
import {
  extensionsFixture,
  failureGroupsFixture,
  healthSeriesFixture,
  librariesFixture,
  snapshotFixture,
  workersFixture,
} from "../../test/fixtures/queues";

function setup(
  queues: unknown = new Promise(() => {}),
  issues: unknown = { groups: failureGroupsFixture }
) {
  // a function answers each read in turn
  const each = (v: unknown) => (typeof v === "function" ? (v as () => unknown) : () => v);
  routeGet(get, {
    "/api/queues": each(queues),
    "/api/issues": each(issues),
    "/api/queues/libraries": { items: librariesFixture },
    "/api/queues/health": { series: healthSeriesFixture },
    "/api/workers": { workers: workersFixture },
    "/api/extensions": { extensions: extensionsFixture },
    "/api/activity": { items: [], next: null },
  });
  return renderAt(<QueueOverview />, "/queue");
}

describe("QueueOverview on the cluster socket", () => {
  beforeEach(() => {
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    clusterSocket.reset();
    get.mockReset();
    post.mockReset();
    hub.reset();
  });
  afterEach(() => {
    clusterSocket.reset();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("subscribes to queues, jobs and workers and renders the hub's snapshot", async () => {
    setup();
    const ws = FakeSocket.last;
    ws.open();
    expect(ws.subscribed().sort()).toEqual(["jobs", "queues", "workers"]);
    expect(hub.count("queues")).toBe(0);
    ws.recv({ type: "snapshot", topic: "queues", data: snapshotFixture });
    expect(await screen.findByRole("link", { name: "Length" })).toBeInTheDocument();
  });

  it("shows each queue's jobs live with the machine they run on", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    setup();
    const ws = FakeSocket.last;
    ws.open();
    ws.recv({ type: "queues", data: snapshotFixture });
    ws.recv({
      type: "job",
      data: {
        session: "s1",
        ip: "198.51.100.5",
        node_name: "Studio Mac",
        job: {
          event: "progress",
          job_id: 9001,
          stage: "probe",
          subject_kind: "video",
          subject_id: "v9",
          title: "Episode 9",
          attempt: 1,
          started_at: new Date().toISOString(),
          ts: new Date().toISOString(),
          duration_ms: null,
          progress: { step: "read", done: 1, total: 4 },
          outcome: null,
          output: null,
        },
      },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_100);
    });
    const probe = document.querySelector('[data-stage="probe"]') as HTMLElement;
    const live = within(probe).getByRole("list", { name: "Running now in Length" });
    expect(within(live).getByText(/Episode 9/)).toBeInTheDocument();
    expect(within(live).getByText(/on .*Studio Mac/)).toBeInTheDocument();
    expect(within(live).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");

    // the job finishes: it leaves the card and the activity feed says so
    ws.recv({
      type: "job",
      data: {
        session: "s1",
        ip: "198.51.100.5",
        node_name: "Studio Mac",
        job: {
          event: "done",
          job_id: 9001,
          stage: "probe",
          subject_kind: "video",
          subject_id: "v9",
          title: "Episode 9",
          attempt: 1,
          ts: new Date().toISOString(),
          duration_ms: 1200,
        },
      },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_100);
    });
    expect(within(probe).queryByRole("list", { name: "Running now in Length" })).toBeNull();
    const log = screen.getByRole("heading", { name: "Activity" }).closest("section")!;
    expect(within(log).getByText(/done · on .*Studio Mac/)).toBeInTheDocument();
  });

  it("shows a job already running when the page loads (the snapshot's running)", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    setup();
    const ws = FakeSocket.last;
    ws.open();
    ws.recv({ type: "queues", data: snapshotFixture });
    // its claimed event is long gone from the recent events; the hub lists it
    // from the machine's latest metrics
    ws.recv({
      type: "snapshot",
      topic: "jobs",
      data: {
        jobs: [],
        running: [
          {
            session: "s2",
            ip: "198.51.100.6",
            node_name: "Linux Box",
            worker_id: "node-1-7",
            host: "linux",
            job: {
              event: "progress",
              job_id: 9100,
              stage: "probe",
              subject_kind: "video",
              subject_id: "v10",
              title: "Episode 10",
              attempt: null,
              started_at: new Date().toISOString(),
              ts: new Date().toISOString(),
              duration_ms: null,
              progress: { step: "read", done: 2, total: 4 },
              outcome: null,
              output: null,
            },
          },
        ],
      },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_100);
    });
    const probe = document.querySelector('[data-stage="probe"]') as HTMLElement;
    const live = within(probe).getByRole("list", { name: "Running now in Length" });
    expect(within(live).getByText(/Episode 10/)).toBeInTheDocument();
    expect(within(live).getByText(/on .*Linux Box/)).toBeInTheDocument();
    expect(within(live).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "50");
    // a seed is not an event: the activity feed does not report it
    const log = screen.getByRole("heading", { name: "Activity" }).closest("section")!;
    expect(within(log).queryByText(/Episode 10/)).toBeNull();

    // a fresh snapshot (a reconnect) without it: it is no longer running
    ws.recv({ type: "snapshot", topic: "jobs", data: { jobs: [], running: [] } });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_100);
    });
    expect(within(probe).queryByRole("list", { name: "Running now in Length" })).toBeNull();
  });

  it("takes the workers from the socket", async () => {
    setup(Promise.resolve(snapshotFixture));
    const ws = FakeSocket.last;
    ws.open();
    ws.recv({
      type: "workers",
      data: {
        workers: [
          {
            worker_id: "node-n1-1",
            host: "studio",
            online: true,
            last_seen_at: null,
            serves: ["transcribe"],
            serves_label: "speech",
            node_id: "n1",
            node_name: "Studio Mac",
            role: "node",
          },
        ],
      },
    });
    expect(await screen.findByText(/Studio Mac/)).toBeInTheDocument();
    expect(screen.getByText(/speech/)).toBeInTheDocument();
  });

  it("falls back to the SSE stream when the socket cannot connect", async () => {
    setup();
    FakeSocket.last.drop();
    expect(hub.count("queues")).toBe(1);
    expect(hub.count("jobs")).toBe(1);
    act(() => {
      hub.emit("queues", { type: "queues.snapshot", at: "x", payload: snapshotFixture });
    });
    expect(await screen.findByRole("link", { name: "Length" })).toBeInTheDocument();
  });

  // the first REST read can answer after the socket's first push: it is older
  it("an older REST snapshot does not override a newer socket push", async () => {
    const rest = deferred<unknown>();
    setup(rest.promise);
    const ws = FakeSocket.last;
    ws.open();
    ws.recv({ type: "queues", data: { ...snapshotFixture, paused_all: true } });
    expect(await screen.findByRole("button", { name: "Resume all" })).toBeInTheDocument();

    rest.resolve(snapshotFixture);
    await act(async () => {
      await rest.promise;
    });
    expect(screen.getByRole("button", { name: "Resume all" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pause all" })).toBeNull();
  });

  it("a snapshot read that began before Pause all does not show the queues running again", async () => {
    const older = deferred<unknown>();
    const paused = { ...snapshotFixture, paused_all: true };
    // the first read, the one after Retry (held), then every read after Pause all
    const answers: unknown[] = [snapshotFixture, older.promise];
    setup(() => (answers.length ? answers.shift() : paused));
    post.mockResolvedValue({ enqueued: 1, coalesced: 0 });
    FakeSocket.last.open();
    await userEvent.click(await screen.findByRole("button", { name: /Retry 2 \(NAS open p95/ }));
    await waitFor(() =>
      expect(get.mock.calls.filter((c) => c[0] === "/api/queues")).toHaveLength(2)
    );

    await userEvent.click(screen.getByRole("button", { name: "Pause all" }));
    expect(post).toHaveBeenCalledWith("/api/queues/pause-all");
    expect(await screen.findByRole("button", { name: "Resume all" })).toBeInTheDocument();

    older.resolve(snapshotFixture);
    await act(async () => {
      await older.promise;
    });
    expect(screen.getByRole("button", { name: "Resume all" })).toBeInTheDocument();
  });

  // "Needs attention" is read again when the issue count changes: a read on its
  // way when a group is retried still has the group
  it("a Needs attention read that began before a Retry does not bring the group back", async () => {
    const older = deferred<unknown>();
    const all = { groups: failureGroupsFixture };
    const retried = { groups: failureGroupsFixture.filter((g) => g.stage !== "fingerprint") };
    // on mount, at the first snapshot, at the next count (held), then after the retry
    const answers: unknown[] = [all, all, older.promise];
    setup(new Promise(() => {}), () => (answers.length ? answers.shift() : retried));
    post.mockResolvedValue({ enqueued: 2, coalesced: 0 });
    const ws = FakeSocket.last;
    ws.open();
    ws.recv({ type: "queues", data: snapshotFixture });
    const retry = { name: /Retry 2 \(NAS open p95/ };
    expect(await screen.findByRole("button", retry)).toBeInTheDocument();
    await waitFor(() =>
      expect(get.mock.calls.filter((c) => c[0] === "/api/issues")).toHaveLength(2)
    );
    ws.recv({
      type: "queues",
      data: { ...snapshotFixture, issues_count: snapshotFixture.issues_count + 1 },
    });
    await waitFor(() =>
      expect(get.mock.calls.filter((c) => c[0] === "/api/issues")).toHaveLength(3)
    );

    await userEvent.click(screen.getByRole("button", retry));
    expect(post).toHaveBeenCalledWith("/api/queues/fingerprint/retry", { cause: "nas_timeout" });
    await waitFor(() => expect(screen.queryByRole("button", retry)).toBeNull());

    older.resolve(all);
    await act(async () => {
      await older.promise;
    });
    expect(screen.queryByRole("button", retry)).toBeNull();
  });

  it("uses SSE when there is no WebSocket at all", async () => {
    vi.stubGlobal("WebSocket", undefined);
    setup(Promise.resolve(snapshotFixture));
    expect(await screen.findByRole("link", { name: "Length" })).toBeInTheDocument();
    expect(hub.count("queues")).toBe(1);
    expect(FakeSocket.all).toHaveLength(0);
  });
});
