// Admin machines cases from web/test-cases/admin-machines.md (Machines.test.tsx
// mocks lib/auth, so these live in their own file on the shared page harness).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, quietCrash } from "../../test/pageHarness";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch: vi.fn(), delete: del } };
});

import { ApiError, type Problem } from "../../lib/api";
import type { ClusterNode, ClusterView } from "../../lib/clusterApi";
import { Machines } from "./Machines";

const CRASH = "This page hit an error and could not be shown.";
const NOW = new Date().toISOString();
const inMinutes = (m: number) => new Date(Date.now() + m * 60_000).toISOString();

const baseNode: ClusterNode = {
  id: "n1",
  name: "Studio Mac",
  state: "approved",
  os: "macOS",
  arch: "arm64",
  versions: { pipeline: "1.4.0" },
  capabilities: { cpu_count: 10, memory_bytes: 32 * 1024 ** 3, apple_silicon: true },
  storage: { read: "ok", write: "ok", checked_at: NOW, detail: null },
  path_map: { "/mnt/nas": "/Volumes/nas" },
  path_map_source: "inferred",
  drain: false,
  expires_at: null,
  created_at: NOW,
  approved_at: NOW,
  revoked_at: null,
  last_seen_at: NOW,
  online: true,
  workers: [
    {
      id: "node-n1-1",
      host: "studio",
      last_seen_at: NOW,
      stages: ["transcribe"],
      stages_off: { thumb_small: "home_only", tag: "switched_off" },
    },
  ],
  stage_overrides: { tag: false },
};

const home = {
  instance_id: "i1",
  name: "Home NAS box",
  accept_joins: false,
  accept_joins_until: null,
  roles_supported: true,
  roles_reason: null,
};

const pendingNode: ClusterNode = {
  ...baseNode,
  id: "p1",
  name: "Linux box",
  state: "pending",
  os: "Linux",
  arch: "x86_64",
  expires_at: inMinutes(10),
  online: false,
  workers: [],
  stage_overrides: {},
  approved_at: null,
};

function fixture(): ClusterView {
  return { home: { ...home }, nodes: [{ ...pendingNode }, { ...baseNode }] };
}

const relayBase = {
  available: true,
  enabled: true,
  settings: {
    requests_per_minute: 60,
    burst: 10,
    max_in_flight: 4,
    max_response_mb: 50,
    timeout_sec: 30,
  },
  extensions: [],
  home_only: [],
  calls: [],
  totals: [],
};

type Answer = unknown | ((url: string) => unknown);
let routes: Record<string, Answer>;

function route(over: Record<string, Answer> = {}) {
  routes = {
    "/api/cluster": fixture(),
    "/api/cluster/relay": relayBase,
    "/api/cluster/files": { files: [], nodes: [] },
    "/api/cluster/storage-servers": { servers: [] },
    ...over,
  };
}

function problem(p: Record<string, unknown>): ApiError {
  return new ApiError({ type: "about:blank", title: "Bad Request", ...p } as unknown as Problem);
}

const open = (user?: typeof memberUser | null) =>
  renderPage(
    <ToastProvider>
      <Machines />
    </ToastProvider>,
    { path: "/admin/machines", ...(user !== undefined ? { user } : {}) }
  );

const clusterCalls = () => get.mock.calls.filter(([u]) => u === "/api/cluster").length;
const card = (name: string | RegExp) => screen.findByRole("article", { name });

describe("Machines (test-cases/admin-machines.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    put.mockReset();
    del.mockReset();
    route();
    get.mockImplementation((url: string) => {
      const path = url.split("?")[0];
      const v = path in routes ? routes[path] : {};
      return Promise.resolve(typeof v === "function" ? (v as (u: string) => unknown)(url) : v);
    });
    post.mockResolvedValue({});
    put.mockResolvedValue({});
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    document.documentElement.dir = "ltr";
  });

  it("MCH-02: non-admin sees Admins only; no cluster request, no socket", async () => {
    const ws = vi.fn();
    vi.stubGlobal("WebSocket", ws);
    open(memberUser);
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
    expect(ws).not.toHaveBeenCalled();
  });

  it("MCH-02: signed-out user is sent to /login", async () => {
    open(null);
    expect(await screen.findByText("login page (from /admin/machines)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it.each([
    ["500", problem({ status: 500 })],
    ["403", problem({ status: 403 })],
    ["401", problem({ status: 401 })],
    ["network", new TypeError("Failed to fetch")],
    ["bad JSON", new SyntaxError("Unexpected end of JSON input")],
  ])("MCH-04: first load fails (%s): alert banner, Try again loads", async (_, err) => {
    const user = userEvent.setup();
    let fail = true;
    route({ "/api/cluster": () => (fail ? Promise.reject(err) : fixture()) });
    open();
    const banner = await screen.findByText("Couldn't load the machines.");
    expect(banner.closest('[role="alert"]')).not.toBeNull();
    // the relay door card still renders below
    expect(screen.getByRole("heading", { name: "Extension relay" })).toBeInTheDocument();
    fail = false;
    const before = clusterCalls();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText(/Home NAS box/)).toBeInTheDocument();
    expect(clusterCalls()).toBe(before + 1);
    expect(screen.queryByText("Couldn't load the machines.")).toBeNull();
  });

  it("MCH-05: a later poll failing shows the stale line, keeps cards, recovers", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    route({
      "/api/cluster": () => (n++ === 1 ? Promise.reject(problem({ status: 500 })) : fixture()),
    });
    open();
    const studio = await card("Studio Mac");
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(
      await screen.findByText("Couldn't refresh — showing the last known state.")
    ).toBeInTheDocument();
    expect(screen.getByRole("article", { name: "Studio Mac" })).toBe(studio);
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    await waitFor(() =>
      expect(screen.queryByText("Couldn't refresh — showing the last known state.")).toBeNull()
    );
  });

  it("MCH-06: nodes [] shows both empty texts and the home card", async () => {
    route({ "/api/cluster": { home, nodes: [] } });
    open();
    expect(await screen.findByText("No machine is asking to join.")).toBeInTheDocument();
    expect(screen.getByText("No machine has joined yet.")).toBeInTheDocument();
    expect(screen.getByText(/Home NAS box/)).toBeInTheDocument();
  });

  it.each([
    ["nodes null", { home, nodes: null }],
    ["nodes missing", { home }],
  ])("MCH-07: %s shows empty lists, not an error", async (_, body) => {
    route({ "/api/cluster": body });
    open();
    expect(await screen.findByText("No machine is asking to join.")).toBeInTheDocument();
    expect(screen.getByText("No machine has joined yet.")).toBeInTheDocument();
    expect(screen.queryByText("Couldn't load the machines.")).toBeNull();
    expect(screen.getByText(/Home NAS box/)).toBeInTheDocument();
  });

  it("MCH-07: a later poll with nodes null empties the lists instead of going stale", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let n = 0;
    route({ "/api/cluster": () => (n++ === 0 ? fixture() : { home, nodes: null }) });
    open();
    await card("Studio Mac");
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(await screen.findByText("No machine has joined yet.")).toBeInTheDocument();
    expect(screen.queryByText("Couldn't refresh — showing the last known state.")).toBeNull();
  });

  it.each([
    ["home null", { home: null, nodes: [] }],
    ["home missing", { nodes: [] }],
  ])("MCH-08: %s keeps the rest of the page", async (_, body) => {
    route({ "/api/cluster": body });
    open();
    expect(await screen.findByText("No machine is asking to join.")).toBeInTheDocument();
    expect(screen.getByText("No machine has joined yet.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "This home" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Extension relay" })).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("MCH-08: home with null name, bad date and empty roles_reason renders", async () => {
    route({
      "/api/cluster": {
        home: {
          ...home,
          name: null,
          accept_joins: true,
          accept_joins_until: "not-a-date",
          roles_supported: false,
          roles_reason: "",
        },
        nodes: [],
      },
    });
    open();
    expect(await screen.findByText("This home can't take other machines.")).toBeInTheDocument();
    expect(screen.queryByText(/Open for another/)).toBeNull();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  const w = baseNode.workers![0];
  it.each([
    [
      "workers null",
      { workers: null, stage_overrides: {} },
      "No worker has reported in from this machine yet.",
    ],
    [
      "workers missing",
      { workers: undefined, stage_overrides: {} },
      "No worker has reported in from this machine yet.",
    ],
    ["worker stages null", { workers: [{ ...w, stages: null }] }, null],
    ["worker stages_off null", { workers: [{ ...w, stages_off: null }] }, null],
    ["stage_overrides null", { stage_overrides: null }, null],
    [
      "storage shares null",
      { storage: { read: "ok", write: "ok", shares: null, checked_at: null, detail: null } },
      null,
    ],
    ["storage null", { storage: null }, "Not checked yet."],
    ["storage {}", { storage: {} }, "Not checked yet."],
    ["path_map null", { path_map: null, path_map_source: null }, "No share is mounted here yet."],
    ["capabilities null", { capabilities: null }, null],
    ["capabilities.gpus null", { capabilities: { gpus: null } }, null],
  ])("MCH-09: %s renders the card", async (_, patch, text) => {
    const node = { ...baseNode, ...patch } as unknown as ClusterNode;
    route({ "/api/cluster": { home, nodes: [node] } });
    open();
    const c = await card("Studio Mac");
    if (text) expect(within(c).getByText(text)).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
    expect(screen.queryByText("Couldn't load the machines.")).toBeNull();
  });

  it("MCH-09: a node with name null still lists, no load banner", async () => {
    route({
      "/api/cluster": {
        home,
        nodes: [
          { ...baseNode, name: null },
          { ...baseNode, id: "n2", name: "B" },
        ],
      },
    });
    open();
    expect(await card("B")).toBeInTheDocument();
    expect(screen.queryByText("Couldn't load the machines.")).toBeNull();
    expect(screen.getAllByRole("article")).toHaveLength(2);
  });

  const storedHome = {
    ...home,
    storage: { read: "ok", write: "ok", checked_at: null, detail: null },
  };
  it.each([
    ["servers null", { servers: null }],
    ["servers missing", {}],
    ["body null", null],
    ["request fails", () => Promise.reject(problem({ status: 500 }))],
  ])("MCH-10: storage servers %s shows the none text", async (_, body) => {
    route({
      "/api/cluster": { home: storedHome, nodes: [] },
      "/api/cluster/storage-servers": body,
    });
    open();
    expect(
      await screen.findByText("No storage server yet. Paths stay as the home stores them.")
    ).toBeInTheDocument();
  });

  it("MCH-10: a server with addresses null renders and edits with empty addresses", async () => {
    const user = userEvent.setup();
    route({
      "/api/cluster": { home: storedHome, nodes: [] },
      "/api/cluster/storage-servers": {
        servers: [{ id: "s1", name: "nas", addresses: null, char_map: "none", exports: {} }],
      },
    });
    open();
    const sect = await screen.findByRole("region", { name: "Storage servers" });
    expect(await within(sect).findByText("smb://nas/")).toBeInTheDocument();
    expect(within(sect).getByText("-")).toBeInTheDocument();
    await user.click(within(sect).getByRole("button", { name: "Edit nas" }));
    expect(within(sect).getByLabelText("Addresses (IPs, host names, .local name)")).toHaveValue("");
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it.each([
    ["extensions null", { extensions: null }],
    ["home_only null", { home_only: null }],
    ["calls null", { calls: null }],
    ["totals null", { totals: null }],
    ["calls missing", { calls: undefined }],
    [
      "extension allow null",
      { extensions: [{ name: "plex", configured: true, enabled: true, allow: null }] },
    ],
    ["settings null", { settings: null }],
  ])("MCH-11: relay %s: door card On and Relay tab render", async (_, patch) => {
    const user = userEvent.setup();
    route({ "/api/cluster/relay": { ...relayBase, ...patch } });
    open();
    const door = (await screen.findByRole("heading", { name: "Extension relay" })).closest(
      "section"
    )!;
    expect(await within(door).findByText("On")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Relay" }));
    expect(await screen.findByText("No relayed calls since the home started.")).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("MCH-12: canonicalize problems null and servers_added addresses null render", async () => {
    route({
      "/api/cluster": {
        home: {
          ...storedHome,
          canonicalize: {
            state: {
              phase: "done",
              problems: null,
              servers_added: [{ name: "nas", addresses: null }],
            },
          },
        },
        nodes: [],
      },
    });
    open();
    expect(await screen.findByText(/smb:\/\/nas\/ \(\)/)).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("MCH-14: accept-joins toggle failure shows a toast and re-enables", async () => {
    const user = userEvent.setup();
    put.mockRejectedValue(problem({ status: 500, title: "Internal Server Error", detail: "nope" }));
    open();
    const sw = await screen.findByRole("switch", { name: "Accept new machines" });
    await user.click(sw);
    expect(await screen.findByText("nope")).toBeInTheDocument();
    await waitFor(() => expect(sw).toBeEnabled());
  });

  async function approveWith(code: string) {
    const user = userEvent.setup();
    const input = await screen.findByLabelText(/Code shown on/);
    await user.clear(input);
    await user.type(input, code);
    await user.click(screen.getByRole("button", { name: "Approve" }));
  }

  it.each([
    ["wrong code, no count", problem({ status: 400, code: "wrong_code" }), "Wrong code."],
    [
      "wrong code, 0 left (type URI)",
      problem({ status: 400, type: "https://x/problems/wrong_code", attempts_left: 0 }),
      "Wrong code, 0 tries left.",
    ],
    [
      "expired code (400)",
      problem({ status: 400, code: "expired" }),
      "This request has expired. Ask again from the machine.",
    ],
    ["other error", problem({ status: 500, title: "Internal Server Error", detail: "db" }), "db"],
    ["network", new TypeError("Failed to fetch"), "Failed to fetch"],
  ])("MCH-15: approve fails (%s)", async (_, err, msg) => {
    post.mockRejectedValue(err);
    open();
    await approveWith("123456");
    expect(await screen.findByRole("alert")).toHaveTextContent(msg);
  });

  it("MCH-15: 0 tries left reloads the cluster", async () => {
    post.mockRejectedValue(problem({ status: 400, code: "wrong_code", attempts_left: 0 }));
    open();
    await screen.findByText(/Home NAS box/);
    const before = clusterCalls();
    await approveWith("123456");
    await waitFor(() => expect(clusterCalls()).toBeGreaterThan(before));
  });

  it.each([
    ["123-456", "123456"],
    ["١٢٣٤٥٦", "123456"],
  ])("MCH-15: code %s is accepted as %s", async (code, digits) => {
    open();
    await approveWith(code);
    expect(post).toHaveBeenCalledWith("/api/cluster/nodes/p1/approve", { code: digits });
  });

  it.each(["12345a", "1234567"])("MCH-15: code %s is refused client-side", async (code) => {
    open();
    await approveWith(code);
    expect(await screen.findByText("Enter the 6-digit code.")).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("MCH-15: Approve is disabled while empty or whitespace and while busy", async () => {
    const user = userEvent.setup();
    post.mockImplementation(() => new Promise(() => {}));
    open();
    const input = await screen.findByLabelText(/Code shown on/);
    const approve = screen.getByRole("button", { name: "Approve" });
    expect(approve).toBeDisabled();
    await user.type(input, "   ");
    expect(approve).toBeDisabled();
    await user.clear(input);
    await user.type(input, "123456");
    await user.click(approve);
    expect(approve).toBeDisabled();
    expect(post).toHaveBeenCalledTimes(1);
  });

  it("MCH-16: an expired pending request disables the form", async () => {
    route({
      "/api/cluster": {
        home,
        nodes: [{ ...pendingNode, expires_at: new Date(Date.now() - 60_000).toISOString() }],
      },
    });
    open();
    expect(await screen.findByText("Expired")).toBeInTheDocument();
    expect(screen.getByLabelText(/Code shown on/)).toBeDisabled();
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
  });

  it("MCH-17: revoke Cancel / Escape send nothing; backdrop does not close", async () => {
    const user = userEvent.setup();
    open();
    const c = await card("Studio Mac");
    await user.click(within(c).getByRole("button", { name: "Revoke" }));
    let dlg = await screen.findByRole("dialog", { name: /Revoke .*Studio Mac.*\?/ });
    expect(dlg).toHaveTextContent(/loses access to the database at once/);
    await user.click(within(dlg).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(within(c).getByRole("button", { name: "Revoke" }));
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(within(c).getByRole("button", { name: "Revoke" }));
    dlg = await screen.findByRole("dialog");
    await user.pointer({ keys: "[MouseLeft]", target: dlg.parentElement! });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("MCH-17: revoke failure shows a toast; card stays Online", async () => {
    const user = userEvent.setup();
    post.mockRejectedValue(
      problem({ status: 500, title: "Internal Server Error", detail: "busy" })
    );
    open();
    const c = await card("Studio Mac");
    await user.click(within(c).getByRole("button", { name: "Revoke" }));
    const dlg = await screen.findByRole("dialog");
    await user.click(within(dlg).getByRole("button", { name: "Revoke" }));
    expect(post).toHaveBeenCalledWith("/api/cluster/nodes/n1/revoke");
    expect(await screen.findByText("busy")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(within(c).getByText("Online")).toBeInTheDocument();
  });

  it("MCH-24: a render crash shows the route error fallback; Back works; polling stops", async () => {
    const user = userEvent.setup();
    quietCrash();
    route({
      "/api/cluster": {
        home,
        nodes: [{ ...baseNode, vod: { fallback: true, detail: {} } }],
      },
    });
    open();
    expect(await screen.findByText(CRASH)).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    route();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await card("Studio Mac")).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("MCH-25: cluster and relay polling stop after leaving the page", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    open();
    await card("Studio Mac");
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    const total = get.mock.calls.length;
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(get.mock.calls.length).toBe(total);
  });

  it.each([
    ["files and nodes null", { files: null, nodes: null }, "No machine has saved files yet."],
    [
      "node lists null",
      { nodes: [{ ip: "10.0.0.5", processes: null, dates: null, archived_dates: null }] },
      null,
    ],
    [
      "request fails",
      () => Promise.reject(problem({ status: 500 })),
      "The files could not be read.",
    ],
  ])("MCH-13: files %s: Logs, Queues and Library tabs render", async (_, body, text) => {
    const user = userEvent.setup();
    route({ "/api/cluster/files": body });
    open();
    await card("Studio Mac");
    await user.click(screen.getByRole("tab", { name: "Logs" }));
    if (text) expect(await screen.findByText(text)).toBeInTheDocument();
    else expect(await screen.findByRole("tabpanel")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Queues" }));
    await user.click(screen.getByRole("tab", { name: "Library" }));
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("MCH-18: path map editor trims, drops half rows, removes rows, cancels", async () => {
    const user = userEvent.setup();
    open();
    const c = await card("Studio Mac");
    const pm = within(c).getByRole("region", { name: "Share → local mount" });
    await user.click(within(pm).getByRole("button", { name: "Edit mounts" }));
    await user.click(within(pm).getByRole("button", { name: "Add a share" }));
    await user.type(
      within(pm).getByLabelText("Share (smb://server/share/), row 2"),
      " smb://nas/x "
    );
    await user.click(within(pm).getByRole("button", { name: "Add a share" }));
    await user.type(within(pm).getByLabelText("Share (smb://server/share/), row 3"), " a ");
    await user.type(within(pm).getByLabelText("Local mount on this machine, row 3"), " b ");
    await user.click(within(pm).getByRole("button", { name: "Remove row 1" }));
    await user.click(within(pm).getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith("/api/cluster/nodes/n1", { path_map: { a: "b" } });
    put.mockClear();
    await user.click(within(pm).getByRole("button", { name: "Edit mounts" }));
    await user.click(within(pm).getByRole("button", { name: "Cancel" }));
    expect(within(pm).getByRole("button", { name: "Edit mounts" })).toBeInTheDocument();
    expect(put).not.toHaveBeenCalled();
  });

  it("MCH-19: storage servers: blank name ignored, addresses parsed, delete, failure", async () => {
    const user = userEvent.setup();
    route({
      "/api/cluster": { home: storedHome, nodes: [] },
      "/api/cluster/storage-servers": {
        servers: [{ name: "home-nas", addresses: ["10.0.0.2"], char_map: "none" }],
      },
    });
    del.mockResolvedValue(undefined);
    open();
    const sect = await screen.findByRole("region", { name: "Storage servers" });
    await within(sect).findByText("smb://home-nas/");
    await user.click(within(sect).getByRole("button", { name: "Add a server" }));
    await user.click(within(sect).getByRole("button", { name: "Save" }));
    expect(put).not.toHaveBeenCalled();
    expect(within(sect).getByLabelText("Name used in paths")).toBeInTheDocument();
    await user.type(within(sect).getByLabelText("Name used in paths"), " nas2 ");
    await user.type(
      within(sect).getByLabelText("Addresses (IPs, host names, .local name)"),
      "10.0.0.3, nas2.local 10.0.0.4,,"
    );
    await user.selectOptions(within(sect).getByLabelText(/Names with/), "sfm");
    await user.click(within(sect).getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith("/api/cluster/storage-servers/nas2", {
      addresses: ["10.0.0.3", "nas2.local", "10.0.0.4"],
      char_map: "sfm",
    });
    await waitFor(() => expect(within(sect).queryByLabelText("Name used in paths")).toBeNull());
    await user.click(within(sect).getByRole("button", { name: "Remove home-nas" }));
    expect(del).toHaveBeenCalledWith("/api/cluster/storage-servers/home-nas");
    put.mockRejectedValueOnce(problem({ status: 400, detail: "bad address" }));
    await user.click(within(sect).getByRole("button", { name: "Add a server" }));
    await user.type(within(sect).getByLabelText("Name used in paths"), "x");
    await user.click(within(sect).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("bad address")).toBeInTheDocument();
    expect(within(sect).getByLabelText("Name used in paths")).toHaveValue("x");
  });

  it("MCH-20: offline, draining, rejected and worker-only badges", async () => {
    const twoHoursAgo = new Date(Date.now() - 2 * 3600_000).toISOString();
    route({
      "/api/cluster": {
        home,
        nodes: [
          { ...baseNode, id: "a", name: "Away", online: false, last_seen_at: twoHoursAgo },
          { ...baseNode, id: "b", name: "Drainer", drain: true },
          { ...baseNode, id: "c", name: "Nope", state: "rejected", online: false },
          { ...baseNode, id: "d", name: "Worker", full_node: false },
        ],
      },
    });
    open();
    const away = await card("Away");
    expect(within(away).getByText("Offline")).toBeInTheDocument();
    expect(within(away).getByText(/last seen/)).toBeInTheDocument();
    expect(within(await card("Drainer")).getByText("Draining")).toBeInTheDocument();
    const nope = await card("Nope");
    expect(within(nope).getByText("Rejected")).toBeInTheDocument();
    expect(within(nope).getByRole("button", { name: "Remove" })).toBeInTheDocument();
    expect(within(nope).queryByRole("button", { name: "Revoke" })).toBeNull();
    expect(within(nope).queryByRole("switch")).toBeNull();
    const worker = await card("Worker");
    expect(within(worker).getByText(/Worker only/)).toBeInTheDocument();
    expect(within(worker).queryByText(/last seen/)).toBeNull();
  });

  it("MCH-26: Arabic names are isolated; code input and path rows stay LTR", async () => {
    localStorage.setItem("mkt:locale", "ar");
    route({
      "/api/cluster": {
        home: { ...home, name: "خادم البيت" },
        nodes: [
          { ...pendingNode, name: "حاسوب المكتب" },
          { ...baseNode, path_map: { "smb://nas/أفلام": "/Volumes/أفلام" } },
        ],
      },
    });
    open();
    expect(await screen.findByRole("heading", { level: 1, name: "الأجهزة" })).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    const code = screen.getByRole("textbox", { name: /حاسوب المكتب/ });
    expect(code).toHaveAttribute("dir", "ltr");
    expect(code.getAttribute("aria-labelledby") ?? code.id).toBeTruthy();
    const label = document.querySelector(`label[for="${code.id}"]`)!;
    expect(label.textContent).toContain("⁨حاسوب المكتب⁩");
    expect(screen.getByText("smb://nas/أفلام").closest("li")).toHaveAttribute("dir", "ltr");
  });
});
