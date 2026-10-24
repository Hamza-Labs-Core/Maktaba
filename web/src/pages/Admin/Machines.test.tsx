// Epic 31 — /admin/machines: the home card, pending join requests
// (approve with the pairing code) and the joined machines.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, delete: del } };
});

const { authState } = vi.hoisted(() => ({
  authState: { user: { username: "admin", is_admin: true } },
}));
vi.mock("../../lib/auth", () => ({
  useAuth: () => authState,
  useOptionalAuth: () => authState,
}));

import { ApiError, type Problem } from "../../lib/api";
import type { ClusterNode, ClusterView } from "../../lib/clusterApi";
import { Machines } from "./Machines";
import { WorkersStrip } from "../../components/queue/WorkersStrip";
import { axeViolations } from "../../test/axe";

const inMinutes = (m: number) => new Date(Date.now() + m * 60_000).toISOString();
const NOW = new Date().toISOString();

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

function fixture(): ClusterView {
  return {
    home: {
      instance_id: "i1",
      name: "Home NAS box",
      accept_joins: false,
      accept_joins_until: null,
      roles_supported: true,
      roles_reason: null,
    },
    nodes: [
      {
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
      },
      baseNode,
    ],
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

function problem(p: Record<string, unknown>): ApiError {
  return new ApiError({ type: "about:blank", title: "Bad Request", ...p } as unknown as Problem);
}

describe("Machines admin page", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
    put.mockReset();
    del.mockReset();
    get.mockImplementation(() => Promise.resolve(fixture()));
    post.mockResolvedValue({});
    put.mockResolvedValue({});
    authState.user = { username: "admin", is_admin: true };
  });
  afterEach(() => {
    document.documentElement.dir = "ltr";
  });

  it("renders the home, the pending request and the machine", async () => {
    renderPage();
    expect(await screen.findByText(/Home NAS box/)).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/cluster");
    const accept = screen.getByRole("switch", { name: "Accept new machines" });
    expect(accept).not.toBeChecked();
    expect(screen.getByText("Linux · x86_64")).toBeInTheDocument();
    expect(screen.getByLabelText(/Code shown on/)).toBeInTheDocument();

    const card = screen.getByRole("article", { name: "Studio Mac" });
    expect(within(card).getByText("Online")).toBeInTheDocument();
    expect(within(card).getByText("10 CPUs")).toBeInTheDocument();
    expect(within(card).getByText("32 GB memory")).toBeInTheDocument();
    expect(within(card).getByText("Apple Silicon")).toBeInTheDocument();
    expect(within(card).getByText("worked out automatically")).toBeInTheDocument();
    expect(within(card).getByText("/Volumes/nas")).toBeInTheDocument();
    expect(within(card).getByText("Pulls from it")).toBeInTheDocument();
    expect(within(card).getByText(/runs on the home only/)).toBeInTheDocument();
  });

  it("explains why joins are unavailable and disables the toggle", async () => {
    const v = fixture();
    v.home.roles_supported = false;
    v.home.roles_reason = "A SQLite home cannot take machines.";
    get.mockResolvedValue(v);
    renderPage();
    expect(await screen.findByText("A SQLite home cannot take machines.")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Accept new machines" })).toBeDisabled();
  });

  it("turns accepting new machines on", async () => {
    renderPage();
    await userEvent.click(await screen.findByRole("switch", { name: "Accept new machines" }));
    expect(put).toHaveBeenCalledWith("/api/cluster/settings", { accept_joins: true });
  });

  it("shows the time left while joins are open", async () => {
    const v = fixture();
    v.home.accept_joins = true;
    v.home.accept_joins_until = inMinutes(30);
    get.mockResolvedValue(v);
    renderPage();
    expect(await screen.findByText(/Open for another/)).toBeInTheDocument();
  });

  it("shows the tries left after a wrong code", async () => {
    post.mockRejectedValueOnce(problem({ status: 400, code: "wrong_code", attempts_left: 3 }));
    renderPage();
    await userEvent.type(await screen.findByLabelText(/Code shown on/), "111111");
    await userEvent.click(screen.getByRole("button", { name: "Approve" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Wrong code, 3 tries left.");
  });

  it("says when the request has expired", async () => {
    post.mockRejectedValueOnce(problem({ status: 410, code: "expired" }));
    renderPage();
    await userEvent.type(await screen.findByLabelText(/Code shown on/), "111111");
    await userEvent.click(screen.getByRole("button", { name: "Approve" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/has expired/);
  });

  it("approves with the code's digits only", async () => {
    renderPage();
    await userEvent.type(await screen.findByLabelText(/Code shown on/), "123 456");
    await userEvent.click(screen.getByRole("button", { name: "Approve" }));
    expect(post).toHaveBeenCalledWith("/api/cluster/nodes/p1/approve", { code: "123456" });
  });

  it("refuses a code that is not 6 digits without calling the API", async () => {
    renderPage();
    await userEvent.type(await screen.findByLabelText(/Code shown on/), "12345");
    await userEvent.click(screen.getByRole("button", { name: "Approve" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Enter the 6-digit code.");
    expect(post).not.toHaveBeenCalled();
  });

  it("rejects a request", async () => {
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "Reject" }));
    expect(post).toHaveBeenCalledWith("/api/cluster/nodes/p1/reject");
  });

  it("switches a stage off for a machine", async () => {
    renderPage();
    const select = await screen.findByRole("combobox", { name: /^Pull from transcribe on/ });
    expect(select).toHaveValue("auto");
    expect(screen.getByRole("combobox", { name: /^Pull from tag on/ })).toHaveValue("off");
    await userEvent.selectOptions(select, "off");
    expect(put).toHaveBeenCalledWith("/api/cluster/nodes/n1", { stages: { transcribe: false } });
  });

  it("sets a stage back to automatic with null", async () => {
    renderPage();
    await userEvent.selectOptions(
      await screen.findByRole("combobox", { name: /^Pull from tag on/ }),
      "auto"
    );
    expect(put).toHaveBeenCalledWith("/api/cluster/nodes/n1", { stages: { tag: null } });
  });

  it("drains a machine", async () => {
    renderPage();
    await userEvent.click(await screen.findByRole("switch", { name: /Drain/ }));
    expect(put).toHaveBeenCalledWith("/api/cluster/nodes/n1", { drain: true });
  });

  it("edits the path map and resets it to automatic", async () => {
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "Edit mounts" }));
    const to = screen.getByLabelText("Local mount on this machine, row 1");
    await userEvent.clear(to);
    await userEvent.type(to, "/Volumes/media");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith("/api/cluster/nodes/n1", {
      path_map: { "/mnt/nas": "/Volumes/media" },
    });

    const v = fixture();
    v.nodes[1] = { ...baseNode, path_map_source: "admin" };
    get.mockResolvedValue(v);
    renderPage();
    const cards = await screen.findAllByRole("button", { name: "Reset to automatic" });
    await userEvent.click(cards[0]);
    expect(put).toHaveBeenCalledWith("/api/cluster/nodes/n1", { path_map: null });
  });

  it("shows each share a machine checked (story 31.3)", async () => {
    const v = fixture();
    v.nodes[1] = {
      ...baseNode,
      storage: {
        read: "error",
        write: "ok",
        checked_at: NOW,
        detail: null,
        shares: [
          {
            share: "smb://home-nas/Originals",
            local: "O:/",
            source: "drive",
            read: "ok",
            write: "ok",
            detail: null,
          },
          {
            share: "smb://home-nas/Music",
            local: null,
            source: null,
            read: "error",
            write: "skipped",
            detail: "not mounted here",
          },
        ],
      },
    };
    get.mockResolvedValue(v);
    renderPage();
    const shares = await screen.findByRole("list", { name: "Shares" });
    expect(within(shares).getByText("smb://home-nas/Originals")).toBeInTheDocument();
    expect(within(shares).getByText("O:/")).toBeInTheDocument();
    expect(within(shares).getByText("not mounted here")).toBeInTheDocument();
  });

  it("edits the home's mounts and its storage servers (story 31.3)", async () => {
    const v = fixture();
    v.home = {
      ...v.home,
      storage: {
        read: "ok",
        write: "ok",
        checked_at: NOW,
        detail: null,
        path_map: { "smb://home-nas/Originals/": "/Volumes/Originals/" },
      },
      path_map: null,
    };
    get.mockImplementation((url: string) =>
      Promise.resolve(
        url === "/api/cluster/storage-servers"
          ? {
              servers: [
                {
                  id: "s1",
                  name: "home-nas",
                  addresses: ["192.168.50.94"],
                  char_map: "none",
                  exports: {},
                },
              ],
            }
          : v
      )
    );
    renderPage();
    expect(await screen.findByText("smb://home-nas/")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Edit home-nas" }));
    const addrs = screen.getByLabelText("Addresses (IPs, host names, .local name)");
    await userEvent.clear(addrs);
    await userEvent.type(addrs, "192.168.50.10, home-nas.local");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith("/api/cluster/storage-servers/home-nas", {
      addresses: ["192.168.50.10", "home-nas.local"],
      char_map: "none",
    });
    const edits = screen.getAllByRole("button", { name: "Edit mounts" });
    await userEvent.click(edits[0]);
    const to = screen.getByLabelText("Local mount on this machine, row 1");
    await userEvent.clear(to);
    await userEvent.type(to, "/Volumes/O");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(put).toHaveBeenCalledWith("/api/cluster/home/path-map", {
      path_map: { "smb://home-nas/Originals/": "/Volumes/O" },
    });
  });

  it("asks before revoking a machine", async () => {
    renderPage();
    await userEvent.click(await screen.findByRole("button", { name: "Revoke" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent(/loses access to the database/);
    expect(post).not.toHaveBeenCalled();
    await userEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));
    expect(post).toHaveBeenCalledWith("/api/cluster/nodes/n1/revoke");
  });

  it("removes only revoked or rejected machines", async () => {
    const v = fixture();
    v.nodes[1] = { ...baseNode, state: "revoked", online: false };
    get.mockResolvedValue(v);
    del.mockResolvedValue(undefined);
    renderPage();
    const card = await screen.findByRole("article", { name: "Studio Mac" });
    expect(within(card).getByText("Revoked")).toBeInTheDocument();
    expect(within(card).queryByRole("button", { name: "Revoke" })).toBeNull();
    await userEvent.click(within(card).getByRole("button", { name: "Remove" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    expect(del).toHaveBeenCalledWith("/api/cluster/nodes/n1");
  });

  it("is gated behind admin", async () => {
    authState.user = { username: "viewer", is_admin: false };
    renderPage();
    expect(await screen.findByText(/admins only|forbidden|permission/i)).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("shows a machine paused for the home's database schema (en + ar)", async () => {
    const why = "paused: home database at 0119, this machine needs 0124 — update the home";
    const pausedNode: ClusterNode = {
      ...baseNode,
      workers: [{ ...baseNode.workers[0], stages: [], stages_off: { transcribe: why, tag: why } }],
    };
    get.mockImplementation(() =>
      Promise.resolve({ ...fixture(), nodes: [pausedNode] } satisfies ClusterView)
    );
    const first = renderPage();
    const card = await screen.findByRole("article", { name: "Studio Mac" });
    expect(within(card).getByText("Paused")).toBeInTheDocument();
    expect(within(card).getByRole("status")).toHaveTextContent(why);
    first.unmount();

    localStorage.setItem("mkt:locale", "ar");
    renderPage();
    const ar = await screen.findByRole("article", { name: "Studio Mac" });
    expect(within(ar).getByRole("status")).toHaveTextContent(
      "متوقف مؤقتًا: قاعدة بيانات الجهاز الرئيسي على الإصدار 0119 وهذا الجهاز يحتاج الإصدار 0124"
    );
  });

  it("links each machine to its web address, with its version and kind (Epic 35)", async () => {
    const v = fixture();
    v.home.web_url = "http://home.local:8780";
    v.home.version = "1.5.0";
    v.nodes[1] = {
      ...baseNode,
      web_url: "http://studio.local:8780",
      version: "1.5.0",
      platform: "darwin-arm64",
      full_node: false,
      vod: {},
      update_state: {},
    };
    get.mockResolvedValue(v);
    renderPage();
    const card = await screen.findByRole("article", { name: "Studio Mac" });
    const link = within(card).getByRole("link", { name: /Studio Mac.*opens in a new tab/ });
    expect(link).toHaveAttribute("href", "http://studio.local:8780");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(within(card).getByText("Version 1.5.0 · darwin-arm64")).toBeInTheDocument();
    expect(
      within(card).getByText("Worker only — re-run the installer once (install-node … -Full)")
    ).toBeInTheDocument();
    expect(within(card).queryByText(/VOD served by/)).not.toBeInTheDocument();

    const home = screen.getByRole("link", { name: /home\.local/ });
    expect(home).toHaveAttribute("href", "http://home.local:8780");
    expect(screen.getByText("Version 1.5.0")).toBeInTheDocument();
  });

  it("marks a full machine and never links an address that is not http(s)", async () => {
    const v = fixture();
    v.nodes[1] = { ...baseNode, web_url: "javascript:alert(1)", full_node: true };
    get.mockResolvedValue(v);
    renderPage();
    const card = await screen.findByRole("article", { name: "Studio Mac" });
    expect(within(card).getByText("Full machine")).toBeInTheDocument();
    expect(
      within(card).queryByRole("link", { name: /opens in a new tab/ })
    ).not.toBeInTheDocument();
  });

  it("says when another machine serves VOD because the share is not readable here", async () => {
    const v = fixture();
    v.nodes[1] = {
      ...baseNode,
      full_node: true,
      vod: {
        fallback: true,
        served_by: "Home NAS box",
        detail: "smb://nas/Originals: permission denied",
        checked_at: NOW,
      },
    };
    get.mockResolvedValue(v);
    const first = renderPage();
    const card = await screen.findByRole("article", { name: "Studio Mac" });
    const note = within(card).getByRole("note");
    expect(note).toHaveTextContent(/VOD served by .*Home NAS box.*: share not readable here/);
    expect(note).toHaveTextContent("smb://nas/Originals: permission denied");
    first.unmount();

    localStorage.setItem("mkt:locale", "ar");
    renderPage();
    const ar = await screen.findByRole("article", { name: "Studio Mac" });
    expect(within(ar).getByRole("note")).toHaveTextContent(
      /الفيديو عند الطلب: المجلد المشترك غير قابل للقراءة هنا/
    );
    expect(within(ar).getByText("جهاز كامل")).toBeInTheDocument();
  });

  it("renders in Arabic, right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderPage();
    expect(await screen.findByRole("switch", { name: "قبول أجهزة جديدة" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "الأجهزة" })).toBeInTheDocument();
    expect(screen.getByText("طلبات الانضمام")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "موافقة" })).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
  });

  it("has no axe violations", async () => {
    const { container } = renderPage();
    await screen.findByRole("article", { name: "Studio Mac" });
    expect(await axeViolations(container)).toEqual([]);
  });

  it("re-reads the cluster every 5 seconds", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      renderPage();
      await screen.findByText(/Home NAS box/);
      const before = get.mock.calls.length;
      await vi.advanceTimersByTimeAsync(5_000);
      expect(get.mock.calls.length).toBeGreaterThan(before);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("WorkersStrip", () => {
  it("names a joined machine and links admins to the Machines page", () => {
    render(
      <I18nProvider>
        <MemoryRouter>
          <WorkersStrip
            workers={[
              {
                worker_id: "node-n1-1",
                host: "studio",
                online: true,
                last_seen_at: NOW,
                serves: ["transcribe"],
                serves_label: "transcription",
                node_id: "n1",
                node_name: "Studio Mac",
                role: "node",
              },
            ]}
          />
        </MemoryRouter>
      </I18nProvider>
    );
    expect(screen.getByRole("link", { name: /Studio Mac/ })).toHaveAttribute(
      "href",
      "/admin/machines"
    );
    expect(screen.getByRole("link", { name: "Manage machines" })).toBeInTheDocument();
  });
});

// A full machine that cannot reach the home's database (its machine agent's home_db):
// not "Online", why on its card, and its logs one click away (update-attempts.md).
describe("Machines admin page: the home's database", () => {
  const at = "2026-10-24T05:30:00Z";

  function routesWith(nodes: ClusterNode[]) {
    get.mockImplementation((url: string) =>
      url.startsWith("/api/cluster/files")
        ? Promise.resolve({ files: null, nodes: [] })
        : Promise.resolve({ ...fixture(), nodes } satisfies ClusterView)
    );
  }

  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    authState.user = { username: "admin", is_admin: true };
  });
  afterEach(() => {
    window.history.replaceState({}, "", "/");
  });

  const unreachable: ClusterNode = {
    ...baseNode,
    full_node: true,
    update_state: {
      home_db: {
        ok: false,
        code: "no_hba",
        detail: 'pq: no pg_hba.conf entry for host "192.168.1.20", user "maktaba_node_n1"',
        at,
        healing: true,
      },
    },
  };

  it("does not show it as healthy, says why, and opens its logs", async () => {
    routesWith([unreachable]);
    renderPage();
    const card = await screen.findByRole("article", { name: "Studio Mac" });
    expect(within(card).queryByText("Online")).toBeNull();
    expect(within(card).getByText("Can't reach the home's database")).toBeInTheDocument();
    const note = within(card).getByTestId("machine-home-db");
    expect(note).toHaveTextContent(
      "This machine can't reach the home's database: the home's database does not let this machine in"
    );
    expect(note).toHaveTextContent("The home gave it new access; it is following it.");
    expect(note).toHaveTextContent("no pg_hba.conf entry");
    // its own log files around the last try, on the log page (over its channel)
    expect(within(note).getByRole("link", { name: "See the logs" })).toHaveAttribute(
      "href",
      `/admin/logs?node=${unreachable.id}&since=2026-10-24T05%3A28%3A00.000Z`
    );
  });

  it("a machine that reaches it stays Online, with no note (and reads in Arabic)", async () => {
    routesWith([{ ...baseNode, update_state: { home_db: { ok: true, at } } }]);
    const first = renderPage();
    const card = await screen.findByRole("article", { name: "Studio Mac" });
    expect(within(card).getByText("Online")).toBeInTheDocument();
    expect(within(card).queryByTestId("machine-home-db")).toBeNull();
    first.unmount();

    routesWith([unreachable]);
    localStorage.setItem("mkt:locale", "ar");
    renderPage();
    const ar = await screen.findByRole("article", { name: "Studio Mac" });
    expect(within(ar).getByTestId("machine-home-db")).toHaveTextContent(
      "لا يستطيع هذا الجهاز الوصول إلى قاعدة بيانات الخادم الرئيسي"
    );
  });
});
