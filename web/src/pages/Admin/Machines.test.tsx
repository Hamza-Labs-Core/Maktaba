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
    await userEvent.click(await screen.findByRole("button", { name: "Edit path map" }));
    const to = screen.getByLabelText("Path on this machine, row 1");
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
