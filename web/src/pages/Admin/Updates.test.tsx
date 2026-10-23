// Story 35.13 (the parts that show, pause and retry a run; install-kinds.md): the Updates
// page lists every machine with its version, install kind and state, follows the run
// under way step by step, and offers Cancel (before the switch), Retry, Skip, Roll back
// and Resume; it pauses and resumes automatic updates.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import { AdminUpdates, stateTone } from "./Updates";

const target = { version: "1.5.0", tag: "v1.5.0", channel: "stable", verify_state: "verified" };

function overview(over: Record<string, unknown> = {}) {
  return {
    current_version: "1.4.0",
    watermark: "1.4.0",
    policy: { auto_install: true, channel: "stable", window: "03:00-05:00" },
    auto_install_available: true,
    plan: {
      target,
      held: false,
      stepping_stone: false,
      auto_install: true,
      due: false,
      next_run_at: "2026-10-23T03:00:00Z",
    },
    catalog: [],
    check_stale: false,
    time_zone: "UTC",
    machines: [
      {
        id: "home",
        name: "home",
        role: "home",
        version: "1.4.0",
        state: "behind",
        kind: "appbundle",
      },
      {
        id: "n1",
        name: "studio-pc",
        role: "node",
        version: "1.4.0",
        state: "behind",
        platform: "windows-amd64",
        kind: "appbundle",
      },
      {
        id: "n2",
        name: "nas-box",
        role: "node",
        version: "",
        state: "offline",
        platform: "linux-amd64",
      },
    ],
    ...over,
  };
}

const liveRun = {
  id: "run-1",
  version: "1.5.0",
  from_version: "1.4.0",
  trigger: "auto",
  state: "home_prepared",
  step: "waiting_tv",
  started_at: "2026-10-23T03:00:00Z",
};

const runView = {
  ...liveRun,
  install_kind: "appbundle",
  detail: { waiting: "tv_busy" },
  steps: [
    { seq: 0, host: "home", step: "preflight", state: "done", started_at: "2026-10-23T03:00:00Z" },
    { seq: 1, host: "home", step: "stage", state: "done", started_at: "2026-10-23T03:00:05Z" },
    {
      seq: 2,
      host: "home",
      step: "rehearse",
      state: "done",
      started_at: "2026-10-23T03:01:00Z",
      detail: { detail: "1 migrations in 2.0 s" },
    },
  ],
  targets: [],
};

function routes(ov: unknown, view: unknown, runs: unknown[] = []) {
  get.mockImplementation((path: string) => {
    if (path === "/api/cluster/updates") return Promise.resolve(ov);
    if (path.startsWith("/api/cluster/updates/runs?")) return Promise.resolve({ items: runs });
    if (path.startsWith("/api/cluster/updates/runs/")) return Promise.resolve(view);
    return Promise.reject(new Error("unexpected " + path));
  });
}

function renderPage(lang?: string) {
  if (lang) localStorage.setItem("mkt:locale", lang);
  return render(
    <I18nProvider>
      <ToastProvider>
        <AdminUpdates />
      </ToastProvider>
    </I18nProvider>
  );
}

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  put.mockReset();
  localStorage.clear();
});

describe("Admin Updates", () => {
  it("lists every machine with its version, install kind and state, and the next update", async () => {
    routes(overview(), null);
    renderPage();
    const table = await screen.findByTestId("updates-machines");
    expect(within(table).getByTestId("updates-machine-home")).toHaveTextContent("The home");
    expect(within(table).getByTestId("updates-machine-home")).toHaveTextContent("desktop app");
    expect(within(table).getByTestId("updates-machine-n1")).toHaveTextContent("studio-pc");
    expect(within(table).getByTestId("updates-machine-n1")).toHaveTextContent("behind");
    expect(within(table).getByTestId("updates-machine-n2")).toHaveTextContent("offline");
    expect(screen.getByTestId("updates-next")).toHaveTextContent("Version 1.5.0 installs on");
    expect(screen.getByTestId("updates-cluster-version")).toHaveTextContent("1.4.0");
  });

  it("follows a run under way: its steps, what it waits on, and Cancel before the switch", async () => {
    routes(overview({ run: liveRun }), runView);
    post.mockResolvedValue({ cancelled: true });
    const user = userEvent.setup();
    renderPage();
    const run = await screen.findByTestId("updates-run");
    expect(run).toHaveTextContent("Updating to 1.5.0");
    expect(run).toHaveTextContent("Waiting until nobody watches TV.");
    expect(screen.getByTestId("updates-home-steps")).toHaveTextContent(
      "Rehearse the database changes"
    );
    expect(screen.getByTestId("updates-home-steps")).toHaveTextContent("1 migrations in 2.0 s");
    expect(screen.getByRole("button", { name: "Update now" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Cancel this update" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/runs/run-1/cancel", {})
    );
  });

  it("offers no Cancel once the home switches", async () => {
    routes(overview({ run: { ...liveRun, state: "home_applying" } }), {
      ...runView,
      state: "home_applying",
    });
    renderPage();
    await screen.findByTestId("updates-run");
    expect(screen.queryByRole("button", { name: "Cancel this update" })).toBeNull();
  });

  it("desktop apps that wait for a click: each with its version, the run naming them, Cancel before the go", async () => {
    const waiting = { ...liveRun, state: "home_applying", step: "waiting_click" };
    const machines = [
      {
        id: "home",
        name: "home",
        role: "home",
        version: "1.4.0",
        state: "waiting_click",
        kind: "appbundle",
        target: "1.5.0",
      },
      {
        id: "n1",
        name: "studio-pc",
        role: "node",
        version: "1.4.0",
        state: "clicked",
        kind: "appbundle",
        target: "1.5.0",
      },
    ];
    routes(overview({ run: waiting, machines }), {
      ...runView,
      ...waiting,
      detail: { home: { state: "awaiting_click" } },
    });
    post.mockResolvedValue({ cancelled: true });
    const user = userEvent.setup();
    renderPage();
    expect(await screen.findByTestId("updates-machine-home-click")).toHaveTextContent(
      "Maktaba 1.5.0 is ready on this machine: it installs when someone clicks Install in Maktaba there"
    );
    expect(screen.getByTestId("updates-machine-home")).toHaveTextContent("waiting for a click");
    expect(screen.getByTestId("updates-machine-n1-click")).toHaveTextContent(
      "Someone chose to install 1.5.0 there"
    );
    expect(await screen.findByTestId("updates-run-waiting-click")).toHaveTextContent(
      "Waiting for a click in Maktaba on: The home"
    );
    await user.click(screen.getByRole("button", { name: "Cancel this update" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/runs/run-1/cancel", {})
    );
  });

  it("a held rollout: the failed machine and the held one, Retry and Resume", async () => {
    const held = {
      ...runView,
      state: "held",
      finished_at: "2026-10-23T03:40:00Z",
      detail: {},
      targets: [
        {
          node_id: "n1",
          name: "studio-pc",
          ord: 0,
          platform: "windows-amd64",
          kind: "appbundle",
          state: "rolled_back",
          from_version: "1.4.0",
          to_version: "1.5.0",
          attempts: 1,
          error: { code: "app_install_failed" },
        },
        {
          node_id: "n3",
          name: "office-pc",
          ord: 1,
          platform: "windows-amd64",
          kind: "appbundle",
          state: "held",
          from_version: "1.4.0",
          to_version: "1.5.0",
          attempts: 0,
          error: { code: "held", held_by: "studio-pc" },
        },
      ],
    };
    routes(
      overview({ last_run: { ...liveRun, state: "held", finished_at: held.finished_at } }),
      held,
      [held]
    );
    post.mockResolvedValue({ run_id: "run-2" });
    const user = userEvent.setup();
    renderPage();
    const failed = await screen.findByTestId("updates-target-n1");
    expect(failed).toHaveTextContent("rolled back");
    expect(failed).toHaveTextContent("app_install_failed");
    expect(screen.getByTestId("updates-target-n3")).toHaveTextContent(
      "studio-pc failed on the same platform"
    );
    await user.click(within(failed).getByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/runs/run-1/targets/n1/retry", {})
    );
    await user.click(screen.getByRole("button", { name: "Update the held machines" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/runs/run-1/resume", {})
    );
    expect(screen.getByTestId("updates-history")).toHaveTextContent("1.4.0 → 1.5.0");
  });

  it("Update now asks first, names the version, then starts it", async () => {
    routes(overview(), null);
    post.mockResolvedValue({ run_id: "run-9", version: "1.5.0" });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Update now" }));
    expect(await screen.findByText("Update the cluster to 1.5.0 now?")).toBeInTheDocument();
    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Update now" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/run", {
        version: "1.5.0",
        confirm: true,
      })
    );
  });

  it("pauses and resumes automatic updates", async () => {
    routes(overview(), null);
    put.mockResolvedValue({ section: "updates", changed: ["auto_install"] });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Pause automatic updates" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/cluster/updates/settings", { auto_install: false })
    );

    routes(
      overview({ policy: { auto_install: false, channel: "stable", window: "03:00-05:00" } }),
      null
    );
    put.mockClear();
    renderPage();
    expect((await screen.findAllByTestId("updates-paused"))[0]).toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "Resume automatic updates" })[0]!);
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/cluster/updates/settings", { auto_install: true })
    );
  });

  it("reads in Arabic", async () => {
    routes(overview(), null);
    renderPage("ar");
    expect(await screen.findByRole("heading", { name: "التحديثات", level: 1 })).toBeInTheDocument();
    expect(screen.getByTestId("updates-machine-home")).toHaveTextContent("الخادم الرئيسي");
  });

  it("colours states", () => {
    expect(stateTone("healthy")).toBe("success");
    expect(stateTone("rolled_back")).toBe("error");
    expect(stateTone("held")).toBe("warn");
    expect(stateTone("preparing")).toBe("accent");
  });
});
