// Epic 51 (story 51.12): one machine's components on /admin/machines (T10) and the
// worker choice in the approve form (T11).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
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

import type { ClusterNode, ClusterView } from "../../lib/clusterApi";
import type { MachineComponent, MachineList } from "../../lib/machineComponentsApi";
import type { Preview } from "../../lib/componentsApi";
import { MachineComponents, machineChange } from "./MachineComponents";
import { Machines } from "./Machines";
import { axeViolations } from "../../test/axe";

const NOW = new Date().toISOString();

function comp(id: string, extra: Partial<MachineComponent> = {}): MachineComponent {
  const base: MachineComponent = {
    id,
    kind: "feature",
    group: "processing",
    title_key: `components.${id}.title`,
    desc_key: `components.${id}.desc`,
    switchable: true,
    placement: "any",
    licensed: true,
    requires: [],
    wants: [],
    conflicts: [],
    dependents: [],
    bundles: ["worker"],
    features: [],
    want: "enabled",
    hold: null,
    generation: 1,
    has_row: true,
    phase: "running",
    expected: { id, from: "running", to: "running" },
    machines: [],
    required_by: [],
    install_size_mb: 0,
    here: {
      want: "enabled",
      hold: null,
      generation: 1,
      override: null,
      phase: "running",
      expected: { id, from: "running", to: "running" },
      reported: true,
      stale: false,
      stages_off: [],
      worker_offer: true,
    },
  };
  return { ...base, ...extra, here: { ...base.here, ...(extra.here ?? {}) } };
}

function machineList(node: string, components: MachineComponent[]): MachineList {
  return {
    revision: 4,
    machine: { id: node, name: "Studio Mac", state: "approved", gpu: true, stale: false },
    components,
    worker_on: 1,
    worker_offered: 2,
  };
}

const quiet: Preview = {
  effects: [{ id: "transcription", from: "running", to: "off" }],
  conflicts: [],
  refused: [],
  queued_jobs_parked: 0,
  drain_s: 0,
};

function renderPanel() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <MachineComponents node="n1" name="Studio Mac" />
      </ToastProvider>
    </I18nProvider>
  );
}

describe("MachineComponents (51.12 T10)", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
    get.mockResolvedValue(
      machineList("n1", [
        comp("transcription", { here: { stages_off: ["transcribe"] } as never }),
        comp("youtube", {
          here: {
            want: "installed",
            phase: "off",
            override: { node: "n1", id: "youtube", want: "installed", generation: 2 },
          } as never,
        }),
        comp("library", { switchable: false, kind: "core" }),
      ])
    );
  });
  afterEach(() => {
    document.documentElement.dir = "ltr";
  });

  it("says a component is on with one stage off, and one off here while on at home", async () => {
    renderPanel();
    const section = await screen.findByRole("region", { name: /^Components on \W?Studio Mac\W?$/ });
    expect(get).toHaveBeenCalledWith("/api/components?machine=n1");
    expect(section.id).toBe("components-n1");
    expect(within(section).getByText("On, 1 stage off: transcribe")).toBeInTheDocument();
    expect(within(section).getByText("Off here (home: on)")).toBeInTheDocument();
    expect(within(section).getByText(/1 of 2 worker components on/)).toBeInTheDocument();
    // a core component is never switched per machine
    expect(within(section).queryByText("library")).toBeNull();
  });

  it("renders in Arabic, right to left, with no axe violations", async () => {
    localStorage.setItem("mkt:locale", "ar");
    const { container } = renderPanel();
    expect(await screen.findByText("مشغّل، مرحلة واحدة متوقفة: transcribe")).toBeInTheDocument();
    expect(screen.getByText("متوقف هنا (الجهاز الرئيسي: مشغّل)")).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("turns a component off on the machine through preview then apply", async () => {
    post.mockImplementation((url: string) =>
      Promise.resolve(
        url.endsWith("/preview") ? quiet : { revision: 5, changed: [], effect: quiet }
      )
    );
    renderPanel();
    await userEvent.click(
      await screen.findByRole("button", { name: "Transcription: off on this machine" })
    );
    const change = { id: "transcription", want: "installed", generation: 0, machine: "n1" };
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/components/apply", { changes: [change] })
    );
    expect(post).toHaveBeenCalledWith("/api/components/preview", { changes: [change] });
  });

  it("follows the home again, clearing a hold the machine has", () => {
    const c = comp("youtube", {
      here: {
        override: { node: "n1", id: "youtube", hold: { by: "maya", at: NOW }, generation: 3 },
      } as never,
    });
    expect(machineChange(c, "follow")).toEqual({
      id: "youtube",
      want: "follow",
      hold: false,
      generation: 3,
    });
  });
});

// ------------------------------------------------------------- approve (T11)

const pending: ClusterNode = {
  id: "p1",
  name: "Linux box",
  state: "pending",
  os: "Linux",
  arch: "x86_64",
  versions: {},
  capabilities: {},
  storage: null,
  path_map: {},
  path_map_source: "inferred",
  drain: false,
  expires_at: new Date(Date.now() + 10 * 60_000).toISOString(),
  created_at: NOW,
  approved_at: null,
  revoked_at: null,
  last_seen_at: null,
  online: false,
  workers: [],
  stage_overrides: {},
} as unknown as ClusterNode;

function cluster(): ClusterView {
  return {
    home: {
      instance_id: "i1",
      name: "Home NAS box",
      accept_joins: true,
      accept_joins_until: null,
      roles_supported: true,
      roles_reason: null,
    },
    nodes: [pending],
  } as ClusterView;
}

describe("approving a worker machine (51.12 T11)", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
    get.mockImplementation((url: string) =>
      Promise.resolve(
        url.startsWith("/api/components?machine=p1")
          ? machineList("p1", [
              comp("transcription", { here: { ram_mb: 4096 } as never }),
              comp("youtube"),
            ])
          : cluster()
      )
    );
    post.mockResolvedValue({});
  });

  it("sends the ticked components, without the one the admin unticked", async () => {
    render(
      <I18nProvider>
        <ToastProvider>
          <MemoryRouter>
            <Machines />
          </MemoryRouter>
        </ToastProvider>
      </I18nProvider>
    );
    const youtube = await screen.findByRole("checkbox", { name: "YouTube" });
    expect(youtube).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Transcription" })).toBeChecked();
    expect(screen.getByText("Needs 4 GB memory")).toBeInTheDocument();
    await userEvent.click(youtube);
    await userEvent.type(screen.getByLabelText(/Code shown on/), "123456");
    await userEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/nodes/p1/approve", {
        code: "123456",
        full: false,
        components: ["transcription"],
      })
    );
  });
});
