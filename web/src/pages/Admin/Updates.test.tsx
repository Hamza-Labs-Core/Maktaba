// Story 35.13 (the parts that show, pause and retry a run; install-kinds.md): the Updates
// page lists every machine with its version, install kind and state, follows the run
// under way step by step, and offers Cancel (before the switch), Retry, Skip, Roll back
// and Resume; it pauses and resumes automatic updates.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, downloadBlob } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  downloadBlob: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put }, downloadBlob };
});

import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
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

/** One machine of a run. */
function machine(node_id: string, name: string, state: string, over: Record<string, unknown> = {}) {
  return {
    node_id,
    name,
    ord: 0,
    platform: "windows-amd64",
    kind: "appbundle",
    state,
    from_version: "1.4.0",
    to_version: "1.5.0",
    attempts: 0,
    ...over,
  };
}

const finishedAt = "2026-10-23T03:40:00Z";

/** A flat install the run cannot update: it needs a person, its detail says what to do. */
const needsPerson = machine("n2", "nas-box", "needs_person", {
  platform: "linux-amd64",
  kind: "flat",
  error: {
    code: "not_self_managed",
    detail:
      "this install has no launcher to fall back with: run `maktaba-server setup --service` once, and it updates itself from then on",
  },
});

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
  downloadBlob.mockReset();
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

  it("a worker only: converted by its next update, or its installer to run once (story 35.8 AC8)", async () => {
    const o = overview();
    const machines: unknown[] = [
      ...o.machines,
      {
        id: "w1",
        name: "old-worker",
        role: "node",
        version: "1.3.0",
        state: "worker_only",
        convert: "installer",
      },
      {
        id: "w2",
        name: "new-worker",
        role: "node",
        version: "1.4.0",
        state: "worker_only",
        convert: "next_update",
      },
      {
        id: "w3",
        name: "converting",
        role: "node",
        version: "1.4.0",
        state: "converting",
        target: "1.5.0",
      },
    ];
    routes({ ...o, machines }, null);
    renderPage();
    const table = await screen.findByTestId("updates-machines");
    expect(within(table).getByTestId("updates-machine-w1-convert")).toHaveTextContent(
      "worker-only — re-run the installer once"
    );
    expect(within(table).getByTestId("updates-machine-w2-convert")).toHaveTextContent(
      "its next update makes it a full node by itself"
    );
    expect(within(table).getByTestId("updates-machine-w3")).toHaveTextContent(
      "becoming a full node"
    );
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
    expect(failed).toHaveTextContent("The desktop app could not install the update");
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

  it("a desktop app waiting for its click, or clicked, may be skipped (its order is withdrawn)", async () => {
    const rolling = { ...liveRun, state: "rolling_out", step: "waiting_click" };
    routes(overview({ run: rolling }), {
      ...runView,
      ...rolling,
      detail: {},
      targets: [
        machine("n1", "studio-pc", "waiting_click"),
        machine("n3", "office-pc", "clicked", { ord: 1 }),
        machine("n4", "den-pc", "applying", { ord: 2 }),
      ],
    });
    post.mockResolvedValue({ skipped: "n1" });
    const user = userEvent.setup();
    renderPage();
    const waiting = await screen.findByTestId("updates-target-n1");
    expect(waiting).toHaveTextContent("waiting for a click");
    const clicked = screen.getByTestId("updates-target-n3");
    expect(within(clicked).getByRole("button", { name: "Skip" })).toBeEnabled();
    // one already installing finishes on its own
    expect(
      within(screen.getByTestId("updates-target-n4")).queryByRole("button", { name: "Skip" })
    ).toBeNull();
    await user.click(within(waiting).getByRole("button", { name: "Skip" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/runs/run-1/targets/n1/skip", {})
    );
  });

  it("a machine that needs a person: what to do (the command), and Skip while the run goes on", async () => {
    const rolling = { ...liveRun, state: "rolling_out", step: "" };
    routes(overview({ run: rolling }), {
      ...runView,
      ...rolling,
      detail: {},
      targets: [needsPerson],
    });
    post.mockResolvedValue({ skipped: "n2" });
    const user = userEvent.setup();
    renderPage();
    const row = await screen.findByTestId("updates-target-n2");
    expect(row).toHaveTextContent("needs you");
    expect(within(row).getByTestId("updates-target-n2-error")).toHaveTextContent(
      "This install cannot update itself yet"
    );
    const fix = within(row).getByTestId("updates-target-n2-error-detail");
    expect(fix).toHaveTextContent("it updates itself from then on");
    expect(within(fix).getByText("maktaba-server setup --service").tagName).toBe("CODE");
    expect(within(row).queryByRole("button", { name: "Retry" })).toBeNull();
    await user.click(within(row).getByRole("button", { name: "Skip" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/runs/run-1/targets/n2/skip", {})
    );
  });

  it("a machine that needs a person may be retried once the run is over", async () => {
    const over = {
      ...runView,
      state: "done_with_failures",
      finished_at: finishedAt,
      detail: {},
      targets: [needsPerson],
    };
    routes(
      overview({
        last_run: { ...liveRun, state: "done_with_failures", finished_at: finishedAt },
      }),
      over,
      [over]
    );
    post.mockResolvedValue({ run_id: "run-2" });
    const user = userEvent.setup();
    renderPage();
    const row = await screen.findByTestId("updates-target-n2");
    expect(within(row).getByText("maktaba-server setup --service")).toBeInTheDocument();
    expect(within(row).queryByRole("button", { name: "Skip" })).toBeNull();
    await user.click(within(row).getByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/runs/run-1/targets/n2/retry", {})
    );
  });

  const applying = { ...liveRun, state: "home_applying", step: "applying" };
  const applyingView = {
    ...runView,
    ...applying,
    detail: { home: { state: "applying" } },
    steps: [
      ...runView.steps,
      { seq: 3, host: "home", step: "ask", state: "done", started_at: "2026-10-23T03:02:00Z" },
      {
        seq: 4,
        host: "home",
        step: "switch",
        state: "running",
        started_at: "2026-10-23T03:09:00Z",
      },
    ],
    targets: [needsPerson],
  };
  const updatingHome = [
    { id: "home", name: "home", role: "home", version: "1.4.0", state: "updating", kind: "layout" },
  ];

  it("names the home's click step and its updating state", async () => {
    routes(overview({ run: applying, machines: updatingHome }), applyingView);
    renderPage();
    expect(await screen.findByTestId("updates-home-steps")).toHaveTextContent(
      "Wait for a click in Maktaba on this machine"
    );
    expect(screen.getByTestId("updates-machine-home")).toHaveTextContent("updating");
  });

  it("reads a run under way in Arabic: no raw key, no English", async () => {
    routes(overview({ run: applying, machines: updatingHome }), applyingView);
    renderPage("ar");
    const steps = await screen.findByTestId("updates-home-steps");
    expect(steps).toHaveTextContent("انتظار نقرة في مكتبة على هذا الجهاز");
    expect(screen.getByTestId("updates-machine-home")).toHaveTextContent("قيد التحديث");
    const row = screen.getByTestId("updates-target-n2");
    expect(row).toHaveTextContent("يحتاجك");
    expect(row).toHaveTextContent("لا يستطيع هذا التثبيت تحديث نفسه بعد");
    expect(screen.getByTestId("updates-run")).not.toHaveTextContent(/updates\.|needs you|ask\b/);
  });

  it("a stop it has words for reads as them with its detail; one it has none for, as its code", async () => {
    const failed = {
      ...runView,
      state: "failed",
      finished_at: finishedAt,
      detail: {},
      error: { code: "home_timeout", detail: "the home did not finish updating within 45 min" },
      targets: [
        machine("n1", "studio-pc", "some_new_state", { error: { code: "something_new" } }),
        machine("n3", "office-pc", "failed_prepare", { ord: 1, error: { code: "stage_failed" } }),
        machine("n4", "den-pc", "failed_prepare", { ord: 2, error: { code: "verify_new_check" } }),
      ],
    };
    routes(
      overview({ last_run: { ...liveRun, state: "failed", finished_at: finishedAt } }),
      failed,
      [failed]
    );
    renderPage();
    const stop = await screen.findByTestId("updates-run-error");
    expect(stop).toHaveTextContent("The home took too long to update");
    expect(screen.getByTestId("updates-run-error-detail")).toHaveTextContent("within 45 min");
    const unknown = screen.getByTestId("updates-target-n1");
    expect(unknown).toHaveTextContent("some new state");
    expect(unknown).toHaveTextContent("Stopped at: something_new");
    expect(screen.getByTestId("updates-target-n3-error")).toHaveTextContent(
      "Download and verify: failed"
    );
    expect(screen.getByTestId("updates-target-n4-error")).toHaveTextContent(
      "The release could not be verified"
    );
  });

  it("has every word of the Updates page in Arabic", () => {
    const enCat = en as Record<string, string>;
    const arCat = ar as Record<string, string>;
    const keys = Object.keys(enCat).filter(
      (k) => k.startsWith("updates.") || k.startsWith("notify.update.")
    );
    expect(keys.length).toBeGreaterThan(150);
    for (const k of keys) expect(arCat[k] ?? "", k).toMatch(/[؀-ۿ]/);
    for (const k of Object.keys(arCat).filter((x) => x.startsWith("updates.")))
      expect(enCat[k], k).toBeTruthy();
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
    expect(stateTone("needs_person")).toBe("warn");
    expect(stateTone("superseded")).toBe("neutral");
  });
});

// update-attempts.md: under each machine, its last update attempt, whoever made it (the
// desktop app's own updater, a release's first start, a run): what happened, why in the
// reader's words, the raw detail, and what to do next.
describe("Admin Updates: the last update attempt", () => {
  const at = new Date(Date.now() - 5 * 60_000).toISOString();

  /** The overview with these attempts on the home (and, optionally, on studio-pc). */
  function withAttempts(home: unknown[], node: unknown[] = []) {
    const ov = overview();
    const machines = (ov.machines as Record<string, unknown>[]).map((m) =>
      m.id === "home" ? { ...m, attempts: home } : m.id === "n1" ? { ...m, attempts: node } : m
    );
    return { ...ov, machines };
  }

  function attempt(over: Record<string, unknown> = {}) {
    return {
      id: "app-1",
      at,
      source: "app",
      trigger: "badge",
      from: "1.4.0",
      to: "1.5.0",
      outcome: "failed",
      ...over,
    };
  }

  it("an installed attempt reads as done, with when, from, to and who", async () => {
    routes(withAttempts([attempt({ outcome: "installed", ended_at: at })]), null);
    renderPage();
    const block = await screen.findByTestId("updates-machine-home-attempt");
    expect(block).toHaveTextContent("Last update");
    expect(screen.getByTestId("updates-machine-home-attempt-headline")).toHaveTextContent(
      "Updated to 1.5.0"
    );
    const meta = screen.getByTestId("updates-machine-home-attempt-meta");
    expect(meta).toHaveTextContent("5 minutes ago");
    expect(meta).toHaveTextContent("1.4.0 → 1.5.0");
    expect(meta).toHaveTextContent("By the desktop app (its update badge)");
    expect(within(block).queryByRole("button", { name: "Download the logs" })).toBeNull();
  });

  it("a failed attempt says why, its step, the app's own words, what to do, and offers the logs", async () => {
    routes(
      withAttempts([
        attempt({
          step: "install",
          code: "needs_admin",
          detail: "Permission denied (os error 13)\nwhile replacing /Applications/Maktaba.app",
          fix: "sudo chown -R $(whoami) /Applications/Maktaba.app",
        }),
      ]),
      null
    );
    downloadBlob.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderPage();
    const headline = await screen.findByTestId("updates-machine-home-attempt-headline");
    expect(headline).toHaveTextContent(
      "The update to 1.5.0 failed: The desktop app cannot be replaced without an administrator's password"
    );
    expect(screen.getByTestId("updates-machine-home-attempt-meta")).toHaveTextContent(
      "Stopped at: Install"
    );
    expect(screen.getByTestId("updates-machine-home-attempt-detail")).toHaveTextContent(
      "Permission denied (os error 13)"
    );
    // what to do, in the reader's words; the machine's own text is not offered to copy
    expect(screen.getByTestId("updates-machine-home-attempt-fix")).toHaveTextContent(
      "What to do: move Maktaba to a folder this user can change"
    );
    expect(screen.queryByDisplayValue(/sudo chown/)).toBeNull();
    expect(screen.queryByText(/sudo chown/)).toBeNull();
    // the reason's entry in the help (Epic 35 story 35.15 AC8)
    expect(screen.getByTestId("updates-machine-home-attempt-help")).toHaveAttribute(
      "href",
      "/help/updates#code-desktop-app"
    );
    // the logs of the attempt's window (the admin bundle, with the log files)
    await user.click(screen.getByRole("button", { name: "Download the logs" }));
    await waitFor(() =>
      expect(downloadBlob).toHaveBeenCalledWith(
        expect.stringMatching(/^\/api\/admin\/logs\/export\?since=/),
        "maktaba-diagnostics.tar.gz"
      )
    );
    expect(String(downloadBlob.mock.calls[0]?.[0])).not.toContain("node=");
  });

  it("a command to copy is the web's own for the reason, never the machine's text", async () => {
    routes(
      withAttempts([
        attempt({ step: "install", code: "quarantined", fix: "rm -rf ~ # not this" }),
        attempt({ id: "app-0", code: "something_brand_new", fix: "rm -rf ~" }),
      ]),
      null
    );
    renderPage();
    const fix = await screen.findByTestId("updates-machine-home-attempt-fix");
    expect(fix).toHaveTextContent("What to do: run this in Terminal, then open Maktaba again");
    expect(
      within(fix).getByDisplayValue("xattr -dr com.apple.quarantine /Applications/Maktaba.app")
    ).toBeInTheDocument();
    expect(screen.queryByDisplayValue(/rm -rf/)).toBeNull();
    expect(screen.queryByText(/rm -rf/)).toBeNull();
  });

  it("a reason with no words of what to do shows the machine's text as what it said", async () => {
    routes(
      withAttempts([attempt({ code: "something_brand_new", fix: "restart the frobnicator" })]),
      null
    );
    renderPage();
    const said = await screen.findByTestId("updates-machine-home-attempt-machine-fix");
    expect(said).toHaveTextContent("The machine suggests: restart the frobnicator");
    expect(screen.queryByDisplayValue("restart the frobnicator")).toBeNull();
    expect(screen.queryByTestId("updates-machine-home-attempt-fix")).toBeNull();
  });

  it("a failure the machine got past (it runs that release now) is history, not red", async () => {
    const ov = withAttempts([attempt({ code: "relaunch_failed", step: "relaunch" })]);
    ov.machines = (ov.machines as Record<string, unknown>[]).map((m) =>
      m.id === "home" ? { ...m, version: "1.5.0" } : m
    );
    routes(ov, null);
    renderPage();
    const block = await screen.findByTestId("updates-machine-home-attempt");
    expect(block).toHaveTextContent("reached later");
    expect(screen.getByTestId("updates-machine-home-attempt-superseded")).toHaveTextContent(
      "This machine runs 1.5.0 now, so this failure no longer holds it back."
    );
    expect(screen.getByTestId("updates-machine-home-attempt-headline")).not.toHaveClass(
      "mkt-error"
    );
    expect(within(block).queryByRole("button", { name: "Download the logs" })).toBeNull();
    expect(screen.queryByTestId("updates-machine-home-attempt-fix")).toBeNull();
  });

  it("a run that failed before its release ran (at stage) is history once the machine runs it", async () => {
    const ov = withAttempts([
      attempt({
        source: "run",
        trigger: "run",
        step: "stage",
        code: "download_failed",
        run_id: "run-9",
      }),
    ]);
    ov.machines = (ov.machines as Record<string, unknown>[]).map((m) =>
      m.id === "home" ? { ...m, version: "1.5.0" } : m
    );
    routes(ov, null);
    renderPage();
    expect(
      await screen.findByTestId("updates-machine-home-attempt-superseded")
    ).toBeInTheDocument();
    expect(screen.getByTestId("updates-machine-home-attempt-headline")).not.toHaveClass(
      "mkt-error"
    );
  });

  it("a failure at the release it runs, after it ran (the pipeline's environment), still holds", async () => {
    const ov = withAttempts([
      attempt({ source: "server", trigger: "start", step: "venv", code: "venv_failed" }),
    ]);
    ov.machines = (ov.machines as Record<string, unknown>[]).map((m) =>
      m.id === "home" ? { ...m, version: "1.5.0" } : m
    );
    routes(ov, null);
    renderPage();
    const headline = await screen.findByTestId("updates-machine-home-attempt-headline");
    expect(headline).toHaveClass("mkt-error");
    expect(screen.queryByTestId("updates-machine-home-attempt-superseded")).toBeNull();
  });

  it("a desktop app's server halted on the release it could not start says it stays stopped", async () => {
    const ov = withAttempts([
      attempt({
        id: "start-1.5.0",
        source: "server",
        trigger: "start",
        outcome: "put_back",
        step: "migrate",
        code: "db_migrate_failed",
      }),
    ]);
    ov.machines = (ov.machines as Record<string, unknown>[]).map((m) =>
      m.id === "home" ? { ...m, version: "1.5.0" } : m
    );
    routes(ov, null);
    renderPage();
    const headline = await screen.findByTestId("updates-machine-home-attempt-headline");
    expect(headline).toHaveTextContent(
      "1.5.0 failed to start: The library database could not be brought up to date for the new release. Its library database is back as it was before the update, and the server stays stopped: install 1.4.0 again, or an update that fixes this."
    );
    expect(headline).not.toHaveTextContent("previous release runs again");
    expect(headline).toHaveClass("mkt-error");
  });

  it("a reason that already says the release before runs again is not told twice", async () => {
    routes(
      withAttempts([
        attempt({ source: "server", trigger: "start", outcome: "put_back", code: "start_failed" }),
      ]),
      null
    );
    renderPage();
    const headline = await screen.findByTestId("updates-machine-home-attempt-headline");
    expect(headline.textContent?.match(/runs again/g)).toHaveLength(1);
  });

  it("the launcher's reasons, a run's states and the start step have words", async () => {
    routes(
      withAttempts(
        [
          attempt({
            source: "server",
            trigger: "start",
            outcome: "rolled_back",
            step: "start",
            code: "launcher_boot_loop",
          }),
          attempt({ id: "a2", outcome: "failed", code: "launcher_refused" }),
          attempt({ id: "a3", outcome: "failed", code: "launcher_pending_timeout" }),
          attempt({ id: "a4", outcome: "failed", code: "previous_app_not_restored" }),
          attempt({ id: "a5", source: "server", outcome: "failed", code: "start_interrupted" }),
        ],
        [
          attempt({ id: "run-1", source: "run", code: "failed_prepare", run_id: "run-1" }),
          attempt({ id: "run-0", source: "run", code: "needs_person", run_id: "run-0" }),
        ]
      ),
      null
    );
    renderPage();
    const headline = await screen.findByTestId("updates-machine-home-attempt-headline");
    expect(headline).toHaveTextContent(
      "1.5.0 did not hold: The new release kept stopping as it started"
    );
    expect(headline.textContent?.match(/runs again/g)).toHaveLength(1);
    expect(screen.getByTestId("updates-machine-home-attempt-meta")).toHaveTextContent(
      "Stopped at: Start the new release"
    );
    const earlier = screen.getByTestId("updates-machine-home-attempt-earlier");
    expect(earlier).toHaveTextContent("The launcher refused the new release");
    expect(earlier).toHaveTextContent("did not confirm it was running in time");
    expect(earlier).toHaveTextContent("could not be put back in its folder");
    expect(earlier).toHaveTextContent("start never finished");
    expect(screen.getByTestId("updates-machine-n1-attempt-headline")).toHaveTextContent(
      "The machine could not get the release ready"
    );
    expect(screen.getByTestId("updates-machine-n1-attempt-earlier")).toHaveTextContent(
      "it needs a person"
    );
    expect(document.body).not.toHaveTextContent(/launcher_|failed_prepare|needs_person|: start\b/);
  });

  it("a release that failed to start and was put back says so; the previous release runs", async () => {
    routes(
      withAttempts([
        attempt({
          id: "start-1.5.0",
          source: "server",
          trigger: "start",
          outcome: "put_back",
          step: "migrate",
          code: "db_migrate_failed",
          detail: 'migrate: ERROR 0150_x.sql: relation "videos" does not exist',
        }),
      ]),
      null
    );
    renderPage();
    expect(await screen.findByTestId("updates-machine-home-attempt-headline")).toHaveTextContent(
      "1.5.0 failed to start: The library database could not be brought up to date for the new release. The previous release runs again."
    );
    const meta = screen.getByTestId("updates-machine-home-attempt-meta");
    expect(meta).toHaveTextContent("By the server, starting the new release (its first start)");
    expect(meta).toHaveTextContent("Stopped at: Update the library database");
  });

  it("a check that failed before the feed named a release says so without a version", async () => {
    routes(
      withAttempts([
        attempt({ from: "", to: "", step: "check", code: "no_network", detail: "dns error" }),
      ]),
      null
    );
    renderPage();
    const headline = await screen.findByTestId("updates-machine-home-attempt-headline");
    expect(headline).toHaveTextContent("The update to a new release failed:");
    expect(screen.getByTestId("updates-machine-home-attempt-meta")).not.toHaveTextContent("→");
  });

  it("an attempt under way says where it is", async () => {
    routes(withAttempts([attempt({ outcome: "running", step: "download" })]), null);
    renderPage();
    expect(await screen.findByTestId("updates-machine-home-attempt-headline")).toHaveTextContent(
      "Updating to 1.5.0"
    );
    expect(screen.getByTestId("updates-machine-home-attempt-meta")).toHaveTextContent(
      "Now: Download"
    );
  });

  it("a reason it has no words for reads as its code; none at all says so", async () => {
    routes(
      withAttempts([attempt({ code: "something_brand_new" }), attempt({ id: "app-0", code: "" })]),
      null
    );
    renderPage();
    expect(await screen.findByTestId("updates-machine-home-attempt-headline")).toHaveTextContent(
      "The update to 1.5.0 failed: something brand new"
    );
    const earlier = screen.getByTestId("updates-machine-home-attempt-earlier");
    expect(earlier).toHaveTextContent("Earlier attempts (1)");
    expect(earlier).toHaveTextContent("The update to 1.5.0 failed: no reason was recorded");
  });

  it("a machine's failed run is retried from its attempt", async () => {
    routes(
      withAttempts(
        [],
        [
          attempt({
            id: "run-7",
            source: "run",
            trigger: "auto",
            outcome: "rolled_back",
            step: "watch",
            code: "watch_timeout",
            run_id: "run-7",
          }),
        ]
      ),
      null
    );
    post.mockResolvedValue({});
    const user = userEvent.setup();
    renderPage();
    const block = await screen.findByTestId("updates-machine-n1-attempt");
    expect(screen.queryByTestId("updates-machine-home-attempt")).toBeNull();
    expect(screen.getByTestId("updates-machine-n1-attempt-headline")).toHaveTextContent(
      "1.5.0 did not hold: The new release never settled"
    );
    expect(screen.getByTestId("updates-machine-n1-attempt-meta")).toHaveTextContent(
      "By the cluster's update (the quiet window)"
    );
    await user.click(within(block).getByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/runs/run-7/targets/n1/retry", {})
    );
  });

  it("reads in Arabic", async () => {
    routes(
      withAttempts([
        attempt({ source: "server", trigger: "start", outcome: "put_back", code: "db_ahead" }),
      ]),
      null
    );
    renderPage("ar");
    const headline = await screen.findByTestId("updates-machine-home-attempt-headline");
    expect(headline).toHaveTextContent("فشل بدء 1.5.0");
    expect(headline).toHaveTextContent("يعمل الإصدار السابق مجدداً");
    expect(screen.getByTestId("updates-machine-home-attempt")).not.toHaveTextContent(
      /updates\.|failed to start/
    );
  });
});

// update-attempts.md: a failure links to its logs (a node's own on the Machines page, the
// home's on the log page) for its time, and a node that cannot reach the home's database
// says why on its row.
describe("Admin Updates: logs links and the home's database", () => {
  const at = "2026-10-24T05:30:00Z";
  const ended = "2026-10-24T05:31:00Z";

  function machinesWith(home: Record<string, unknown>, node: Record<string, unknown>) {
    const ov = overview();
    return {
      ...ov,
      machines: (ov.machines as Record<string, unknown>[]).map((m) =>
        m.id === "home" ? { ...m, ...home } : m.id === "n1" ? { ...m, ...node } : m
      ),
    };
  }

  const failed = {
    id: "app-1",
    at,
    ended_at: ended,
    source: "app",
    trigger: "badge",
    from: "1.4.0",
    to: "1.5.0",
    outcome: "failed",
    code: "no_network",
  };

  it("links a failure to its logs for its time: the home's log page, a node's own logs", async () => {
    routes(machinesWith({ attempts: [failed] }, { attempts: [{ ...failed, id: "app-2" }] }), null);
    renderPage();
    const home = await screen.findByTestId("updates-machine-home-attempt-logs-link");
    expect(home).toHaveTextContent("See the logs");
    expect(home).toHaveAttribute(
      "href",
      "/admin/logs?since=2026-10-24T05%3A28%3A00.000Z&until=2026-10-24T05%3A33%3A00.000Z"
    );
    // a node's: its own log files on the log page, over its channel
    expect(screen.getByTestId("updates-machine-n1-attempt-logs-link")).toHaveAttribute(
      "href",
      "/admin/logs?node=n1&since=2026-10-24T05%3A28%3A00.000Z&until=2026-10-24T05%3A33%3A00.000Z"
    );
    // the reason stays the main thing; the bundle download is still there
    expect(screen.getByTestId("updates-machine-home-attempt-headline")).toHaveTextContent(
      "The update to 1.5.0 failed: The update could not be reached"
    );
    const downloads = screen.getAllByRole("button", { name: "Download the logs" });
    expect(downloads).toHaveLength(2);
    downloadBlob.mockResolvedValue(undefined);
    await userEvent.setup().click(downloads[1] as HTMLElement);
    await waitFor(() =>
      expect(downloadBlob).toHaveBeenCalledWith(
        "/api/admin/logs/export?since=2026-10-24T05%3A28%3A00.000Z&until=2026-10-24T05%3A33%3A00.000Z&node=n1",
        "maktaba-diagnostics.tar.gz"
      )
    );
  });

  it("says why a node cannot reach the home's database, the healing, the detail and its logs", async () => {
    routes(
      machinesWith(
        {},
        {
          state: "up_to_date",
          home_db: {
            ok: false,
            code: "auth_failed",
            detail: 'pq: password authentication failed for user "maktaba_node_n1"',
            at,
            healing: true,
          },
        }
      ),
      null
    );
    renderPage();
    const note = await screen.findByTestId("updates-machine-n1-home-db");
    expect(note).toHaveTextContent(
      "This machine can't reach the home's database: the home's database refused its password"
    );
    expect(note).toHaveTextContent("The home gave it new access; it is following it.");
    expect(note).toHaveTextContent("password authentication failed");
    expect(screen.getByTestId("updates-machine-n1-home-db-logs")).toHaveAttribute(
      "href",
      "/admin/logs?node=n1&since=2026-10-24T05%3A28%3A00.000Z"
    );
    expect(screen.queryByTestId("updates-machine-home-home-db")).toBeNull();
  });

  it("a node that reaches it, or an unknown reason, reads as such (en + ar)", async () => {
    routes(machinesWith({}, { home_db: { ok: false, code: "something_new" } }), null);
    const first = renderPage();
    expect(await screen.findByTestId("updates-machine-n1-home-db")).toHaveTextContent(
      "an error the details say"
    );
    first.unmount();

    routes(machinesWith({}, { home_db: { ok: false, code: "no_hba" } }), null);
    renderPage("ar");
    expect(await screen.findByTestId("updates-machine-n1-home-db")).toHaveTextContent(
      "لا يستطيع هذا الجهاز الوصول إلى قاعدة بيانات الخادم الرئيسي"
    );
  });

  it("shows nothing for a node that reaches the home's database", async () => {
    routes(machinesWith({}, { home_db: { ok: true, at } }), null);
    renderPage();
    await screen.findByTestId("updates-machine-n1");
    expect(screen.queryByTestId("updates-machine-n1-home-db")).toBeNull();
  });
});

// The server audit (H1, H2, M6, L5): why a due release does not start, a blocked release
// tried again or installed now, a release the TV held for several windows installed now,
// and the last failed update retried.
describe("Admin Updates: blocked, stuck and waiting releases", () => {
  it("says why a due release does not start now", async () => {
    routes(
      overview({
        last_tick: { action: "blocked", reason: "recent_crash", version: "1.5.0", at: finishedAt },
      }),
      null
    );
    renderPage();
    expect(await screen.findByTestId("updates-tick")).toHaveTextContent(
      "Not started now: a part of the home's server crashed in the last ten minutes"
    );
  });

  it("a release that waits for a person after its last try says so", async () => {
    routes(
      overview({
        last_tick: { action: "none", reason: "needs_person", version: "1.5.0", at: finishedAt },
      }),
      null
    );
    renderPage();
    expect(await screen.findByTestId("updates-tick")).toHaveTextContent("The last try needs you");
  });

  it("a blocked release is tried again, or installed now", async () => {
    routes(
      overview({
        catalog: [{ ...target, version: "1.4.5", blocked_reason: "rolled_back", attempts: 1 }],
      }),
      null
    );
    post.mockResolvedValue({ unblocked: true, version: "1.4.5" });
    const user = userEvent.setup();
    renderPage();
    const box = await screen.findByTestId("updates-blocked-1.4.5");
    expect(box).toHaveTextContent("1.4.5 is blocked: it was rolled back on the home");
    await user.click(within(box).getByRole("button", { name: "Try it again" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/unblock", { version: "1.4.5" })
    );
    post.mockResolvedValue({ run_id: "run-9", version: "1.4.5" });
    await user.click(within(box).getByRole("button", { name: "Install now" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/run", {
        version: "1.4.5",
        confirm: true,
      })
    );
  });

  it("a release the TV held for several windows is offered to install now", async () => {
    routes(
      overview({ stuck: { version: "1.5.0", code: "tv_busy", windows: 3, since: finishedAt } }),
      null
    );
    post.mockResolvedValue({ run_id: "run-9", version: "1.5.0" });
    const user = userEvent.setup();
    renderPage();
    const box = await screen.findByTestId("updates-stuck");
    expect(box).toHaveTextContent("1.5.0 has not installed in 3 windows");
    await user.click(within(box).getByRole("button", { name: "Install now" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/cluster/updates/run", {
        version: "1.5.0",
        confirm: true,
      })
    );
  });

  it("the last failed update is tried again now", async () => {
    const failed = {
      ...liveRun,
      id: "run-7",
      state: "failed",
      finished_at: finishedAt,
      error: { code: "local_build" },
    };
    routes(overview({ last_run: failed }), null, [failed]);
    renderPage();
    const box = await screen.findByTestId("updates-last-failed");
    expect(box).toHaveTextContent("This app was built locally and can't update itself");
    expect(within(box).getByRole("button", { name: "Try 1.5.0 again now" })).toBeInTheDocument();
  });

  it("reads in Arabic", async () => {
    routes(
      overview({
        stuck: { version: "1.5.0", code: "tv_busy", windows: 3, since: finishedAt },
        catalog: [{ ...target, version: "1.4.5", blocked_reason: "attempts" }],
        last_tick: { action: "blocked", reason: "run_active", version: "1.5.0", at: finishedAt },
      }),
      null
    );
    renderPage("ar");
    expect(await screen.findByTestId("updates-stuck")).toHaveTextContent("ثبّته الآن");
    expect(screen.getByTestId("updates-blocked-1.4.5")).toHaveTextContent("فشل في ثلاث نوافذ");
    expect(screen.getByTestId("updates-tick")).toHaveTextContent("يجري تحديث بالفعل");
  });
});
