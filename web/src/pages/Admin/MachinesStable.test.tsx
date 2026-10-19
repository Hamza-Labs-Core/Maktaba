// The Machines page holds still while it updates: a fixed order, no remount
// and no re-render of an unchanged machine on a poll, the skeleton on the
// first load only, the hub's frames batched to one update a second, and
// each machine's jobs as a count with a link to the filtered queue page.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, post, put, renders } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  renders: { fmt: 0, procs: {} as Record<string, number> },
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});
vi.mock("../../lib/auth", () => {
  const state = { user: { username: "admin", is_admin: true } };
  return { useAuth: () => state, useOptionalAuth: () => state };
});
// render counters: the machine cards' storage / stage parts call
// useClusterFmt once per render; every process row renders its commands
vi.mock("../../components/cluster/format", async () => {
  const actual = await vi.importActual<typeof import("../../components/cluster/format")>(
    "../../components/cluster/format"
  );
  return {
    ...actual,
    useClusterFmt: () => {
      renders.fmt++;
      return actual.useClusterFmt();
    },
  };
});
vi.mock("../../components/cluster/ProcessActions", () => ({
  ProcessActions: ({ process }: { process: { session: string } }) => {
    renders.procs[process.session] = (renders.procs[process.session] ?? 0) + 1;
    return null;
  },
}));

import { Machines } from "./Machines";
import type { ClusterNode, ClusterProcess, ClusterView } from "../../lib/clusterApi";
import { clusterSocket } from "../../lib/clusterSocket";
import { clusterStreamConfig, type LiveProcess } from "../../lib/clusterStream";
import {
  countsKey,
  jobCounts,
  queueHref,
  reconcileView,
  sortNodes,
} from "../../components/cluster/machineSummary";
import { representative } from "../../components/cluster/LiveMachines";
import { FakeSocket } from "../../test/fakeSocket";

const NOW = new Date().toISOString();

function node(id: string, name: string, over: Partial<ClusterNode> = {}): ClusterNode {
  return {
    id,
    name,
    state: "approved",
    os: "macOS",
    arch: "arm64",
    versions: {},
    capabilities: { cpu_count: 8 },
    storage: { read: "ok", write: "ok", checked_at: NOW, detail: null },
    path_map: {},
    path_map_source: "inferred",
    drain: false,
    expires_at: null,
    created_at: NOW,
    approved_at: NOW,
    revoked_at: null,
    last_seen_at: NOW,
    online: true,
    workers: [{ id: `w-${id}`, host: name, last_seen_at: NOW, stages: ["probe"], stages_off: {} }],
    stage_overrides: {},
    ...over,
  };
}

const home = {
  instance_id: "i1",
  name: "Home NAS box",
  accept_joins: false,
  accept_joins_until: null,
  roles_supported: true,
  roles_reason: null,
};

function view(order: "ab" | "ba", over: Partial<ClusterNode> = {}): ClusterView {
  const a = node("na", "Alpha", over);
  const b = node("nb", "Bravo");
  const revoked = node("nz", "Aardvark", { state: "revoked", online: false });
  return { home: { ...home }, nodes: order === "ab" ? [revoked, a, b] : [b, revoked, a] };
}

function proc(session: string, nodeId: string | null, jobs = 0): ClusterProcess {
  return {
    session,
    kind: nodeId ? "node" : "home",
    node_id: nodeId,
    node_name: nodeId ? "Alpha" : null,
    ip: nodeId ? "10.0.0.5" : "10.0.0.2",
    host: nodeId ? "alpha" : "nas",
    worker_id: `${session}-worker`,
    pid: 1,
    connected_at: NOW,
    metrics: {
      system: { cpu_pct: 10, mem_total: 8, mem_available: 4 },
      pipeline: {
        role: nodeId ? "node" : "home",
        jobs: Array.from({ length: jobs }, (_, i) => ({
          job_id: i + 1,
          stage: "probe",
          subject: `Video ${i + 1}`,
          since: NOW,
          progress: 0.5,
        })),
      },
    },
    llm: null,
  };
}

function renderPage() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <MemoryRouter>
          <Machines />
        </MemoryRouter>
      </ToastProvider>
    </I18nProvider>
  );
}

const names = () =>
  screen
    .getAllByRole("article")
    .map((a) => a.getAttribute("aria-label"))
    .filter(Boolean);

describe("machineSummary", () => {
  it("orders machines by state, then name, whatever order the API sends", () => {
    expect(sortNodes(view("ba").nodes).map((n) => n.name)).toEqual(["Alpha", "Bravo", "Aardvark"]);
    expect(sortNodes(view("ab").nodes).map((n) => n.id)).toEqual(
      sortNodes(view("ba").nodes).map((n) => n.id)
    );
  });

  it("keeps the previous objects when a poll brings the same data", () => {
    const first = reconcileView(null, view("ab"));
    expect(reconcileView(first, view("ba"))).toBe(first);
    const next = reconcileView(first, view("ba", { drain: true }));
    expect(next).not.toBe(first);
    expect(next.home).toBe(first.home);
    expect(next.nodes.find((n) => n.id === "nb")).toBe(first.nodes.find((n) => n.id === "nb"));
    expect(next.nodes.find((n) => n.id === "na")).not.toBe(first.nodes.find((n) => n.id === "na"));
  });

  it("counts the jobs per machine from online processes only", () => {
    const live = (p: ClusterProcess, online = true): LiveProcess => ({
      ...p,
      online,
      seenAt: 0,
      disconnectedAt: null,
    });
    const counts = jobCounts({
      h: live(proc("h", null, 2)),
      a1: live(proc("a1", "na", 1)),
      a2: live(proc("a2", "na", 3)),
      gone: live(proc("gone", "nb", 4), false),
    });
    expect(counts).toEqual({ home: 2, na: 4, nb: 0 });
    expect(countsKey(counts)).toBe(countsKey({ nb: 0, na: 4, home: 2 }));
    expect(queueHref("home")).toBe("/queue?host=home");
  });

  it("picks the same process for a machine's tiles whichever reported last", () => {
    const live = (s: string, seenAt: number): LiveProcess => ({
      ...proc(s, "na"),
      worker_id: s,
      online: true,
      seenAt,
      disconnectedAt: null,
    });
    const group = (a: number, b: number) => ({
      key: "node:na",
      kind: "node" as const,
      nodeId: "na",
      name: "Alpha",
      ip: "",
      processes: [live("a-1", a), live("a-2", b)],
    });
    expect(representative(group(1, 2))?.session).toBe("a-1");
    expect(representative(group(2, 1))?.session).toBe("a-1");
  });
});

describe("Machines page stability", () => {
  beforeEach(() => {
    localStorage.clear();
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    clusterSocket.reset();
    get.mockReset();
    renders.fmt = 0;
    renders.procs = {};
  });
  afterEach(() => {
    clusterSocket.reset();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    clusterStreamConfig.flushMs = 1_000;
  });

  it("shows the skeleton on the first load only, then keeps order and DOM across polls", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let poll = 0;
    get.mockImplementation((url: string) => {
      if (url === "/api/cluster") return Promise.resolve(view(poll++ % 2 ? "ba" : "ab"));
      return Promise.reject(new Error(`unmocked ${url}`));
    });
    renderPage();
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(document.querySelector(".mkt-mc-skeleton")).not.toBeNull();
    expect(await screen.findByRole("article", { name: "Alpha" })).toBeInTheDocument();
    expect(names()).toEqual(["Alpha", "Bravo", "Aardvark"]);
    const alpha = screen.getByRole("article", { name: "Alpha" });
    const before = renders.fmt;

    // the next poll sends the machines in another order: nothing moves or re-renders
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(poll).toBeGreaterThanOrEqual(2);
    expect(names()).toEqual(["Alpha", "Bravo", "Aardvark"]);
    expect(screen.getByRole("article", { name: "Alpha" })).toBe(alpha);
    expect(renders.fmt).toBe(before);
    expect(document.querySelector(".mkt-mc-skeleton")).toBeNull();

    // a failed poll says so on the line that is always there, keeping the cards
    get.mockImplementation(() => Promise.reject(new Error("down")));
    const lines = document.querySelectorAll(".mkt-mc-statusline").length;
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(screen.getByText(/Couldn't refresh/)).toBeInTheDocument();
    expect(document.querySelectorAll(".mkt-mc-statusline").length).toBe(lines);
    expect(screen.getByRole("article", { name: "Alpha" })).toBe(alpha);
    expect(document.querySelector(".mkt-mc-skeleton")).toBeNull();
  });

  it("re-renders only the machine whose data changed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let drain = false;
    get.mockImplementation(() => Promise.resolve(view("ab", { drain })));
    renderPage();
    await screen.findByRole("article", { name: "Alpha" });
    const before = renders.fmt;
    drain = true;
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(within(screen.getByRole("article", { name: "Alpha" })).getByText("Draining"));
    // Alpha's storage check and stage table rendered again (2 calls); Bravo's did not
    expect(renders.fmt - before).toBe(2);
  });

  it("shows each machine's jobs as a count linking to the filtered queue page", async () => {
    clusterStreamConfig.flushMs = 0;
    get.mockImplementation(() => Promise.resolve(view("ab")));
    renderPage();
    await screen.findByRole("article", { name: "Alpha" });
    const ws = FakeSocket.last;
    ws.open();
    ws.recv({
      type: "snapshot",
      topic: "processes",
      data: { processes: [proc("h1", null, 1), proc("s1", "na", 2)] },
    });
    const alpha = screen.getByRole("article", { name: "Alpha" });
    expect(within(alpha).getByText("Jobs running: 2")).toBeInTheDocument();
    expect(
      within(alpha).getByRole("link", { name: "View the jobs on Alpha in the queues" })
    ).toHaveAttribute("href", "/queue?host=na&name=Alpha");
    expect(
      within(screen.getByRole("article", { name: "Bravo" })).getByText("Jobs running: 0")
    ).toBeInTheDocument();
    const homeCard = screen.getByRole("region", { name: "This home" });
    expect(within(homeCard).getByText("Jobs running: 1")).toBeInTheDocument();
    expect(within(homeCard).getByRole("link", { name: /View the jobs on/ })).toHaveAttribute(
      "href",
      "/queue?host=home"
    );
    // no per-job progress on this page
    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(screen.queryByText("Video 1")).toBeNull();
  });

  it("batches the hub's frames into one update a second and re-renders only the changed process", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    get.mockImplementation(() => Promise.resolve(view("ab")));
    renderPage();
    await screen.findByRole("article", { name: "Alpha" });
    const ws = FakeSocket.last;
    ws.open();
    ws.recv({
      type: "snapshot",
      topic: "processes",
      data: { processes: [proc("h1", null), proc("s1", "na")] },
    });
    await act(() => vi.advanceTimersByTimeAsync(1_000));
    expect(screen.getByRole("region", { name: "Alpha, live" })).toBeInTheDocument();
    const start = { ...renders.procs };

    for (let i = 0; i < 5; i++)
      ws.recv({
        type: "metrics",
        data: { session: "s1", metrics: { system: { cpu_pct: 20 + i, mem_total: 8 } } },
      });
    // nothing reaches React before the flush
    expect(renders.procs.s1).toBe(start.s1);
    await act(() => vi.advanceTimersByTimeAsync(1_000));
    // five frames, one render of the process that changed, none of the other
    expect(renders.procs.s1).toBe(start.s1 + 1);
    expect(renders.procs.h1).toBe(start.h1);
    expect(within(screen.getByRole("region", { name: "Alpha, live" })).getByText("24%"));
  });
});
