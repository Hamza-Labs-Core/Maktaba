// Epic 31 phase 2 — the live cluster views on /admin/machines, fed by the
// cluster hub's WebSocket (a fake one here) and the files on the share.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});
vi.mock("../../lib/auth", () => {
  const state = { user: { username: "admin", is_admin: true } };
  return { useAuth: () => state, useOptionalAuth: () => state };
});

import { Machines } from "../../pages/Admin/Machines";
import type { ClusterProcess } from "../../lib/clusterApi";
import { clusterSocket } from "../../lib/clusterSocket";
import { clusterStreamConfig } from "../../lib/clusterStream";
import { FakeSocket } from "../../test/fakeSocket";
import { routeGet } from "../../test/queueHarness";
import { axeViolations } from "../../test/axe";

const NOW = new Date().toISOString();
const GB = 1024 ** 3;

const home: ClusterProcess = {
  session: "h1",
  kind: "home",
  node_id: null,
  node_name: null,
  ip: "10.0.0.2",
  host: "nas",
  worker_id: "home-1",
  pid: 100,
  connected_at: NOW,
  metrics: {
    system: {
      cpu_pct: 20,
      load1: 1.5,
      mem_total: 16 * GB,
      mem_available: 8 * GB,
      disks: [{ path: "/data", total: 1000 * GB, free: 400 * GB }],
      net: { rx_bps: 2 * 1024 * 1024, tx_bps: 512 * 1024 },
    },
    files: { dir: "/share/maktaba/cluster", on_share: true, reason: null },
  },
  llm: {
    runtimes: [
      {
        provider: "ollama",
        base_url: "http://127.0.0.1:11434",
        max_parallel: 1,
        models: [
          { name: "qwen2.5:7b", size: 4 * GB, family: "qwen", quant: "Q4_K_M", loaded: true },
        ],
      },
    ],
  },
};

const node: ClusterProcess = {
  session: "s1",
  kind: "node",
  node_id: "n1",
  node_name: "Studio Mac",
  ip: "10.0.0.5",
  host: "studio",
  worker_id: "node-n1-1",
  pid: 4242,
  connected_at: NOW,
  metrics: {
    system: { cpu_pct: 55, mem_total: 32 * GB, mem_available: 16 * GB },
    gpu: { apple: { active_gb: 6.5, cache_gb: 1, peak_gb: 9 } },
    pipeline: {
      role: "node",
      stages: ["transcribe"],
      stages_off: { tag: "home_only" },
      jobs: [{ job_id: 1, stage: "transcribe", subject: "Lecture 4", since: NOW, progress: 0.5 }],
      claims_per_min: 2,
      stt: { audio_sec_per_s: 12.5, files_in_flight: 1 },
    },
    llm: { in_flight: 1, tokens_per_s: 30 },
    files: { dir: "/Users/x/.maktaba/cluster", on_share: false, reason: "storage" },
  },
  llm: {
    runtimes: [
      {
        provider: "lmstudio",
        base_url: "http://127.0.0.1:1234",
        max_parallel: 2,
        models: [
          { name: "qwen2.5:7b", size: null, family: null, quant: null, loaded: false },
          { name: "llama3:8b", size: null, family: null, quant: null, loaded: true },
        ],
      },
    ],
  },
};

const view = {
  home: {
    instance_id: "i1",
    name: "Home NAS box",
    accept_joins: false,
    accept_joins_until: null,
    roles_supported: true,
    roles_reason: null,
  },
  nodes: [],
};

const files = {
  files: { dir: "/share/maktaba/cluster", on_share: true, reason: null },
  nodes: [
    {
      ip: "10.0.0.5",
      processes: [
        {
          worker_id: "node-n1-1",
          node_id: "n1",
          name: "Studio Mac",
          host: "studio",
          os: "macOS",
          pid: 4242,
          role: "node",
          started_at: NOW,
        },
        {
          worker_id: "node-n1-2",
          node_id: "n1",
          name: "Studio Mac",
          host: "studio",
          os: "macOS",
          pid: 4243,
          role: "node",
          started_at: NOW,
        },
      ],
      dates: {
        logs: ["2026-10-17", "2026-10-18"],
        queues: ["2026-10-18"],
        library: ["2026-10-18"],
      },
      // the home's archive keeps days the share has let go (and some it still has)
      archived_dates: { logs: ["2026-09-01", "2026-10-17"] },
    },
  ],
};

function logsRoute(url: string) {
  if (url.includes("before=cursor1"))
    return {
      records: [
        {
          ts: "2026-10-17T08:00:00Z",
          worker_id: "node-n1-1",
          level: "info",
          event: "older line",
          logger: "w",
          fields: {},
        },
      ],
      next_before: null,
    };
  return {
    records: [
      {
        ts: "2026-10-17T09:00:00Z",
        worker_id: "node-n1-1",
        level: "info",
        event: "claimed job",
        logger: "w",
        fields: { job: 1 },
      },
      {
        ts: "2026-10-17T09:01:00Z",
        worker_id: "node-n1-2",
        level: "error",
        event: "probe failed",
        logger: "w",
        fields: {},
      },
    ],
    next_before: "cursor1",
  };
}

function setup(v: unknown = view) {
  routeGet(get, {
    "/api/cluster": v,
    "/api/cluster/files": files,
    "/api/cluster/files/10.0.0.5/logs": logsRoute,
    "/api/cluster/files/10.0.0.5/queues": { samples: [] },
    "/api/cluster/files/10.0.0.5/library": { samples: [] },
    "/api/cluster/relay": {
      available: true,
      enabled: true,
      settings: {},
      extensions: [],
      home_only: [],
      calls: [],
      totals: [],
    },
  });
  const utils = render(
    <I18nProvider>
      <ToastProvider>
        <MemoryRouter>
          <Machines />
        </MemoryRouter>
      </ToastProvider>
    </I18nProvider>
  );
  return utils;
}

/** Renders, opens the socket and sends the processes snapshot. */
async function live(v: unknown = view) {
  const utils = setup(v);
  await screen.findByText(/Home NAS box/);
  const ws = FakeSocket.last;
  ws.open();
  ws.recv({ type: "snapshot", topic: "processes", data: { processes: [home, node] } });
  return { ...utils, ws };
}

const machine = (name: string) => screen.getByRole("region", { name: `${name}, live` });

describe("Cluster views", () => {
  beforeEach(() => {
    localStorage.clear();
    FakeSocket.all = [];
    vi.stubGlobal("WebSocket", FakeSocket);
    clusterSocket.reset();
    // every hub frame applied at once (production batches them once a second)
    clusterStreamConfig.flushMs = 0;
    get.mockReset();
    post.mockReset();
    put.mockReset();
  });
  afterEach(() => {
    clusterSocket.reset();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    clusterStreamConfig.flushMs = 1_000;
    document.documentElement.dir = "ltr";
  });

  // update-attempts.md: connected to the hub, but not reaching the home's database, is
  // not healthy: its live card is not green
  it("a machine that cannot reach the home's database is not green on its live card", async () => {
    const studio = {
      id: "n1",
      name: "Studio Mac",
      state: "approved",
      os: "macOS",
      arch: "arm64",
      versions: {},
      capabilities: {},
      storage: null,
      path_map: null,
      path_map_source: null,
      drain: false,
      expires_at: null,
      created_at: NOW,
      approved_at: NOW,
      revoked_at: null,
      last_seen_at: NOW,
      online: true,
      workers: [],
      stage_overrides: {},
      update_state: { home_db: { ok: false, code: "auth_failed", at: NOW } },
    };
    await live({ ...view, nodes: [studio] });
    await waitFor(() =>
      expect(
        within(machine("Studio Mac")).getByText("Can't reach the home's database")
      ).toBeInTheDocument()
    );
    // and its own log files are a click away (the log page, over its channel)
    expect(
      within(machine("Studio Mac")).getByRole("link", { name: "See the logs" })
    ).toHaveAttribute("href", "/admin/logs?node=n1");
    // its processes are connected to the hub, and say so, but not in green
    for (const badge of within(machine("Studio Mac")).getAllByText("Connected"))
      expect(badge.className).not.toMatch(/success/);
    expect(within(machine("nas")).getAllByText("Connected")[0]!.className).toMatch(/success/);
  });

  it("subscribes to the cluster topics and renders machines and processes from the snapshot", async () => {
    const { ws } = await live();
    expect(ws.subscribed()).toEqual(
      expect.arrayContaining(["processes", "metrics", "llm", "jobs", "queues", "library"])
    );
    expect(screen.getByText("Live")).toBeInTheDocument();
    // the relay door sits between the approvals and the live machines (the extra slot)
    const door = screen.getByRole("region", { name: "Extension relay" });
    expect(await within(door).findByText("On")).toBeInTheDocument();

    const nas = machine("nas");
    expect(within(nas).getByText("Home")).toBeInTheDocument();
    expect(within(nas).getByText("20%")).toBeInTheDocument();
    expect(within(nas).getByText("8 GB of 16 GB")).toBeInTheDocument();
    expect(within(nas).getByText("Disk /data")).toBeInTheDocument();
    expect(within(nas).getByText("Saving its files to the share")).toBeInTheDocument();

    const mac = machine("Studio Mac");
    expect(within(mac).getByText("node-n1-1")).toBeInTheDocument();
    // the jobs are a count with a link to the Processing page, not progress bars
    expect(within(mac).queryByRole("progressbar")).toBeNull();
    expect(within(mac).queryByText(/Lecture 4/)).toBeNull();
    expect(within(mac).getAllByText("Jobs running: 1").length).toBeGreaterThan(0);
    expect(
      within(mac).getByRole("link", { name: "View the jobs on Studio Mac in the queues" })
    ).toHaveAttribute("href", "/queue?host=n1&name=Studio%20Mac");
    expect(
      within(nas).getByRole("link", { name: "View the jobs on nas in the queues" })
    ).toHaveAttribute("href", "/queue?host=home");
    expect(within(mac).getByText("12.5× real time")).toBeInTheDocument();
    expect(within(mac).getByText("30 tokens/s")).toBeInTheDocument();
    expect(within(mac).getByText("6.5 GB")).toBeInTheDocument();
    expect(
      within(mac).getByText("Saving its files locally: storage not reachable")
    ).toBeInTheDocument();
    expect(within(mac).getByText("Pulls from:").closest("p")).toHaveTextContent(
      "Pulls from: transcribe"
    );
    expect(
      within(mac).getByText("Doesn't pull from tag: runs on the home only")
    ).toBeInTheDocument();
  });

  it("updates values and sparklines from metrics events", async () => {
    const { ws } = await live();
    expect(screen.queryByRole("img", { name: "CPU on Studio Mac, last 5 minutes" })).toBeNull();
    ws.recv({
      type: "metrics",
      data: {
        session: "s1",
        metrics: { ...node.metrics, system: { ...node.metrics!.system, cpu_pct: 80 } },
      },
    });
    const mac = machine("Studio Mac");
    expect(within(mac).getByText("80%")).toBeInTheDocument();
    expect(
      within(mac).getByRole("img", { name: "CPU on Studio Mac, last 5 minutes" })
    ).toBeInTheDocument();
  });

  it("shows a disconnected process and disables its commands", async () => {
    const { ws } = await live();
    ws.recv({
      type: "process",
      data: { event: "disconnected", session: "s1", process: null, reason: null },
    });
    const mac = machine("Studio Mac");
    expect(within(mac).getAllByText("Disconnected").length).toBeGreaterThan(0);
    expect(within(mac).getByRole("button", { name: "Drain" })).toBeDisabled();
    expect(within(machine("nas")).getByRole("button", { name: "Drain" })).toBeEnabled();
  });

  it("sends a command over the hub and shows its result", async () => {
    const { ws } = await live();
    await userEvent.click(within(machine("Studio Mac")).getByRole("button", { name: "Drain" }));
    const cmd = ws.sent.find((m) => m.op === "cmd")!;
    expect(cmd).toMatchObject({ session: "s1", name: "drain", args: {} });
    ws.recv({ type: "cmd.result", data: { id: cmd.id, ok: true, detail: "draining" } });
    expect(await screen.findByText("Drain: done. draining")).toBeInTheDocument();

    await userEvent.selectOptions(
      within(machine("Studio Mac")).getByRole("combobox", { name: /Log level of/ }),
      "debug"
    );
    const lvl = ws.sent.filter((m) => m.op === "cmd")[1];
    expect(lvl).toMatchObject({ name: "log_level", args: { level: "debug" } });
    ws.recv({ type: "cmd.result", data: { id: lvl.id, ok: false, detail: "unknown level" } });
    expect(await screen.findByRole("alert")).toHaveTextContent("Log level failed. unknown level");
  });

  it("asks before disconnecting a process", async () => {
    const { ws } = await live();
    await userEvent.click(
      within(machine("Studio Mac")).getByRole("button", { name: "Disconnect" })
    );
    const dialog = await screen.findByRole("dialog");
    expect(ws.sent.some((m) => m.op === "cmd")).toBe(false);
    await userEvent.click(within(dialog).getByRole("button", { name: "Disconnect" }));
    expect(ws.sent.find((m) => m.op === "cmd")).toMatchObject({
      name: "disconnect",
      session: "s1",
    });
  });

  it("tails a process's log live, moving the subscription with the level", async () => {
    const { ws } = await live();
    await userEvent.click(within(machine("Studio Mac")).getByRole("button", { name: "Logs" }));
    expect(screen.getByRole("tab", { name: "Logs" })).toHaveAttribute("aria-selected", "true");
    expect(ws.subscribed()).toContain("logs:s1:info");

    ws.recv({
      type: "log",
      data: {
        session: "s1",
        ip: "10.0.0.5",
        record: {
          ts: Date.now() / 1000,
          level: "info",
          event: "claimed job",
          logger: "worker",
          fields: { job: 7 },
        },
      },
    });
    ws.recv({
      type: "log",
      data: {
        session: "s1",
        ip: "10.0.0.5",
        record: {
          ts: Date.now() / 1000,
          level: "warning",
          event: "slow read",
          logger: "nas",
          fields: {},
        },
      },
    });
    const box = screen.getByRole("log", { name: "Log lines" });
    expect(within(box).getByText("claimed job")).toBeInTheDocument();
    expect(within(box).getByText("job=7")).toBeInTheDocument();

    // search filters what is shown
    await userEvent.type(screen.getByRole("searchbox", { name: "Search" }), "slow");
    expect(within(box).queryByText("claimed job")).toBeNull();
    expect(within(box).getByText("slow read")).toBeInTheDocument();
    await userEvent.clear(screen.getByRole("searchbox", { name: "Search" }));

    // a new level is a new topic, and the tail starts again
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Level" }), "warning");
    expect(ws.subscribed()).toContain("logs:s1:warning");
    expect(ws.unsubscribed()).toContain("logs:s1:info");
    expect(within(box).queryByText("slow read")).toBeNull();
    ws.recv({
      type: "log",
      data: {
        session: "s1",
        ip: "10.0.0.5",
        record: { ts: Date.now() / 1000, level: "info", event: "chatty", logger: "w" },
      },
    });
    ws.recv({
      type: "log",
      data: {
        session: "s1",
        ip: "10.0.0.5",
        record: { ts: Date.now() / 1000, level: "error", event: "boom", logger: "w" },
      },
    });
    expect(within(box).queryByText("chatty")).toBeNull();
    expect(within(box).getByText("boom")).toBeInTheDocument();

    // pause freezes the list
    await userEvent.click(screen.getByRole("button", { name: "Pause" }));
    ws.recv({
      type: "log",
      data: {
        session: "s1",
        ip: "10.0.0.5",
        record: { ts: Date.now() / 1000, level: "error", event: "later", logger: "w" },
      },
    });
    expect(within(box).queryByText("later")).toBeNull();
    expect(screen.getByText("Paused; 1 new lines waiting.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Resume" }));
    expect(within(box).getByText("later")).toBeInTheDocument();
  });

  it("reads an earlier day from the files, loads older lines and filters by process", async () => {
    await live();
    await userEvent.click(screen.getByRole("tab", { name: "Logs" }));
    expect(await screen.findByText("/share/maktaba/cluster")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Open the logs of Studio Mac for 2026-10-17" })
    );
    const box = screen.getByRole("log", { name: "Log lines" });
    expect(await within(box).findByText("claimed job")).toBeInTheDocument();
    expect(within(box).getByText("probe failed")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(
      "/api/cluster/files/10.0.0.5/logs?date=2026-10-17&level=info&limit=500"
    );

    await userEvent.click(screen.getByRole("button", { name: "Load older" }));
    expect(await within(box).findByText("older line")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(
      "/api/cluster/files/10.0.0.5/logs?date=2026-10-17&level=info&limit=500&before=cursor1"
    );
    const rows = within(box).getAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("older line");
    expect(screen.queryByRole("button", { name: "Load older" })).toBeNull();

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Process" }), "node-n1-2");
    expect(within(box).queryByText("claimed job")).toBeNull();
    expect(within(box).getByText("probe failed")).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Level" }), "error");
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        "/api/cluster/files/10.0.0.5/logs?date=2026-10-17&level=error&limit=500"
      )
    );
  });

  it("offers the archive's days with the share's and reads one", async () => {
    await live();
    await userEvent.click(screen.getByRole("tab", { name: "Logs" }));
    expect(await screen.findByText("/share/maktaba/cluster")).toBeInTheDocument();
    // 2026-10-17 is in both: offered once
    expect(
      screen.getAllByRole("button", { name: "Open the logs of Studio Mac for 2026-10-17" })
    ).toHaveLength(1);
    await userEvent.click(
      screen.getByRole("button", { name: "Open the logs of Studio Mac for 2026-09-01" })
    );
    const box = screen.getByRole("log", { name: "Log lines" });
    expect(await within(box).findByText("claimed job")).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith(
      "/api/cluster/files/10.0.0.5/logs?date=2026-09-01&level=info&limit=500"
    );
  });

  it("lists every model with where it runs and the live request feed", async () => {
    const { ws } = await live();
    await userEvent.click(screen.getByRole("tab", { name: "LLM" }));
    const table = screen.getByRole("table");
    const qwen = within(table).getByRole("row", { name: /qwen2\.5:7b/ });
    expect(within(qwen).getByText("nas", { exact: false })).toBeInTheDocument();
    expect(within(qwen).getByText(/Studio Mac/)).toBeInTheDocument();
    expect(within(qwen).getByText("Loaded")).toBeInTheDocument();
    expect(within(qwen).getByText("Not loaded")).toBeInTheDocument();
    expect(within(table).getByRole("row", { name: /llama3:8b/ })).toBeInTheDocument();

    ws.recv({
      type: "llm",
      data: {
        id: "r1",
        model: "qwen2.5:7b",
        op: "chat",
        from_session: "h1",
        to_session: "s1",
        state: "started",
        usage: null,
        error: null,
      },
    });
    expect(
      within(within(table).getByRole("row", { name: /qwen2\.5:7b/ })).getByText("1")
    ).toBeInTheDocument();
    const feed = screen.getByRole("list", { name: "Requests" });
    expect(within(feed).getByText("Running")).toBeInTheDocument();
    expect(within(feed).getByText(/nas.*→.*Studio Mac/)).toBeInTheDocument();

    ws.recv({
      type: "llm",
      data: {
        id: "r1",
        state: "done",
        usage: { prompt_tokens: 12, completion_tokens: 40, ms: 900 },
        error: null,
      },
    });
    expect(within(feed).getByText("Done")).toBeInTheDocument();
    expect(within(feed).getByText("12 in, 40 out, 900 ms")).toBeInTheDocument();
  });

  it("streams an answer from the OpenAI-compatible door", async () => {
    const enc = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(enc.encode('data: {"choices":[{"delta":{"content":"Hello"}}]}\n\n'));
        c.enqueue(
          enc.encode('data: {"choices":[{"delta":{"content":" world"}}]}\n\ndata: [DONE]\n\n')
        );
        c.close();
      },
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(body, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await live();
    await userEvent.click(screen.getByRole("tab", { name: "LLM" }));
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Model" }), "llama3:8b");
    await userEvent.type(screen.getByRole("textbox", { name: "Prompt" }), "Say hi");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() =>
      expect(screen.getByRole("status", { name: "Answer" })).toHaveTextContent("Hello world")
    );
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/cluster/llm/v1/chat/completions");
    expect(JSON.parse(init.body as string)).toEqual({
      model: "llama3:8b",
      stream: true,
      messages: [{ role: "user", content: "Say hi" }],
    });
  });

  it("shows job events with per-queue throughput and filters them", async () => {
    const { ws } = await live();
    await userEvent.click(screen.getByRole("tab", { name: "Jobs" }));
    const ev = (event: string, id: number, stage: string, extra: Record<string, unknown> = {}) =>
      ws.recv({
        type: "job",
        data: {
          session: "s1",
          ip: "10.0.0.5",
          job: {
            event,
            job_id: id,
            stage,
            subject_kind: "video",
            subject_id: `v${id}`,
            title: `Video ${id}`,
            attempt: 1,
            started_at: NOW,
            ts: NOW,
            duration_ms: null,
            progress: null,
            outcome: null,
            output: null,
            ...extra,
          },
        },
      });
    ev("done", 1, "transcribe", { duration_ms: 60_000 });
    ev("done", 2, "transcribe", { duration_ms: 120_000 });
    ev("failed", 3, "probe", { outcome: { kind: "error", retryable: true, error: "NAS timeout" } });
    const stats = screen.getByRole("table");
    const row = within(stats).getByRole("row", { name: /transcribe/ });
    expect(row).toHaveTextContent("2");
    expect(row).toHaveTextContent("0.2");
    expect(row).toHaveTextContent("2 min");
    const feed = screen.getByRole("list", { name: "Job events" });
    expect(within(feed).getAllByRole("listitem")).toHaveLength(3);
    expect(within(feed).getByText("NAS timeout")).toBeInTheDocument();

    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Outcome" }), "failed");
    expect(within(feed).getAllByRole("listitem")).toHaveLength(1);
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Outcome" }), "all");
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Queue" }), "transcribe");
    expect(within(feed).getAllByRole("listitem")).toHaveLength(2);
  });

  it("charts the queues over time from live samples", async () => {
    const { ws } = await live();
    await userEvent.click(screen.getByRole("tab", { name: "Queues" }));
    const snap = (pending: number, running: number, at: string) => ({
      at,
      paused_all: false,
      stages: [{ stage: "transcribe", label: "Speech to text" }],
      queues: [{ stage: "transcribe", counts: { pending, running }, rate_per_min: 3 }],
    });
    ws.recv({ type: "queues", data: snap(10, 1, "2026-10-19T10:00:00Z") });
    ws.recv({ type: "queues", data: snap(8, 2, "2026-10-19T10:00:02Z") });
    expect(
      screen.getByRole("img", { name: "Jobs waiting and running over time, All queues" })
    ).toBeInTheDocument();
    expect(screen.getByText("Waiting:", { exact: false })).toHaveTextContent("Waiting: 8");
    const table = screen.getByRole("table");
    expect(within(table).getByRole("row", { name: /Speech to text/ })).toHaveTextContent("82");
  });

  it("shows the library numbers", async () => {
    const { ws } = await live();
    await userEvent.click(screen.getByRole("tab", { name: "Library" }));
    ws.recv({
      type: "library",
      data: {
        ts: "2026-10-19T10:00:00Z",
        libraries: [
          {
            library_id: "l1",
            name: "Lectures",
            videos: 120,
            hours: 300,
            transcribed_hours: 150,
            tagged: 10,
            indexed: 90,
          },
        ],
        totals: {
          videos: 120,
          hours: 300,
          transcribed_hours: 150,
          transcribed_videos: 60,
          tagged: 10,
          indexed: 90,
          bytes: 2 * 1024 * GB,
        },
      },
    });
    const totals = screen.getByLabelText("Library totals");
    expect(within(totals).getByText("150 (50%)")).toBeInTheDocument();
    expect(within(totals).getByText("2 TB")).toBeInTheDocument();
    expect(screen.getByRole("row", { name: /Lectures/ })).toBeInTheDocument();
  });

  it("renders in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    await live();
    expect(screen.getByRole("tab", { name: "السجلات" })).toBeInTheDocument();
    expect(screen.getByText("يعمل الآن")).toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "Studio Mac، مباشر" })).getAllByText("متصل").length
    ).toBeGreaterThan(0);
    expect(document.documentElement.dir).toBe("rtl");
  });

  it("has no axe violations", async () => {
    const { container } = await live();
    expect(await axeViolations(container)).toEqual([]);
    await userEvent.click(screen.getByRole("tab", { name: "LLM" }));
    expect(await axeViolations(container)).toEqual([]);
    await userEvent.click(screen.getByRole("tab", { name: "Logs" }));
    await screen.findByText("/share/maktaba/cluster");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("says when the live view cannot connect", async () => {
    vi.stubGlobal("WebSocket", undefined);
    setup();
    expect(
      await screen.findByText("The live view cannot connect to the hub right now.")
    ).toBeInTheDocument();
    expect(screen.getByText("Live view unavailable")).toBeInTheDocument();
  });
});
