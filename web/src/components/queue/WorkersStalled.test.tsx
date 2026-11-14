// The Queues page says which workers do not run their jobs and lets an admin restart
// them (the pulse's `workers` block; POST /api/queues/workers/restart), and says when
// the machine is overloaded (the pulse's `host` block). In English and Arabic.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../../lib/api";
import type { HostLoad, Pulse, StalledWorker, WorkersState } from "../../lib/queueApi";
import { PulseBanner } from "./PulseBanner";
import { renderAt, routeGet } from "../../test/queueHarness";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;
const hoursAgo = (h: number) => new Date(Date.now() - h * 3600_000).toISOString();

const worker = (over: Partial<StalledWorker> = {}): StalledWorker => ({
  worker_id: "home/2/stt",
  host: "home-mac",
  node_name: null,
  kind: "transcription",
  component: "transcription",
  reason: "no_dispatch",
  since: hoursAgo(19),
  last_seen_at: new Date(Date.now() - 4000).toISOString(),
  waiting: 40,
  stages: ["transcribe"],
  restartable: true,
  ...over,
});
const stalled = (over: Partial<WorkersState> = {}): WorkersState => ({
  state: "not_responding",
  reason: "no_dispatch",
  since: hoursAgo(19),
  last_seen_at: new Date(Date.now() - 4000).toISOString(),
  waiting: 40,
  stalled: [worker(), worker({ worker_id: "home/3/stt" })],
  can_restart: true,
  ...over,
});
const running: WorkersState = {
  state: "running",
  since: null,
  last_seen_at: hoursAgo(0),
  waiting: 0,
  stalled: [],
  can_restart: true,
};
const pulse = (workers?: WorkersState, over: Partial<Pulse> = {}): Pulse => ({
  state: workers?.state === "not_responding" ? "stalled" : "working",
  running: 2,
  waiting: 40,
  paused_all: false,
  last_activity_at: "2026-05-18T11:59:48Z",
  age_sec: 12,
  stuck: [],
  workers,
  ...over,
});
const overloaded: HostLoad = {
  overloaded: true,
  load1: 512.3,
  load5: 498,
  load15: 300,
  cores: 28,
  bound: 56,
  top: [
    { pid: 900, name: "runaway", cpu_pct: 795.4 },
    { pid: 812, name: "Some App", cpu_pct: 398 },
  ],
};
const show = (p: Pulse) => {
  routeGet(get, { "/api/queues/pulse": p });
  return renderAt(<PulseBanner />, "/queue");
};
const notice = async () =>
  (await screen.findByText(EN["queue.workersStalled.title"])).closest(
    ".mkt-q-pulse"
  ) as HTMLElement;

beforeEach(() => {
  get.mockReset();
  post.mockReset();
});
afterEach(() => {
  localStorage.removeItem("mkt:locale");
  document.documentElement.dir = "ltr";
});

describe("the workers run the jobs", () => {
  it("shows the pulse line and no notice", async () => {
    show(pulse(running));
    expect(await screen.findByText("Last activity 12 s ago")).toBeInTheDocument();
    expect(screen.queryByText(EN["queue.workersStalled.title"])).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("an older server's pulse, without the workers block, shows the line as before", async () => {
    show(pulse(undefined));
    expect(await screen.findByText("Last activity 12 s ago")).toBeInTheDocument();
    expect(screen.queryByText(EN["queue.workersStalled.title"])).toBeNull();
  });
});

describe("some workers are not running their jobs", () => {
  it("names each stalled worker, since when and what happens, while other jobs run", async () => {
    show(
      pulse(
        stalled({
          stalled: [
            worker(),
            worker({
              worker_id: "home/1/main",
              kind: "main",
              component: "core.pipeline",
              reason: "no_heartbeat",
              last_seen_at: hoursAgo(2),
              since: hoursAgo(2),
            }),
            worker({ worker_id: "home/4/index", kind: "search", component: "search.semantic" }),
          ],
        })
      )
    );
    const alert = await notice();
    expect(alert).toHaveAttribute("role", "alert");
    expect(alert).toHaveTextContent("Some workers are not running their jobs.");
    expect(alert).toHaveTextContent(/Since .+ \(19 h ago\)\./);
    const rows = within(alert).getAllByRole("listitem");
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent(
      /Transcription worker on .*home-mac.*: reports in but has started no job for 19 h\./
    );
    expect(rows[1]).toHaveTextContent(/Main worker on .*home-mac.*: has not reported in for 2 h\./);
    expect(rows[2]).toHaveTextContent(/Search worker on /);
    expect(alert).toHaveTextContent(
      "40 jobs are waiting for them. They stay queued and start when the workers run again; nothing is lost."
    );
    expect(alert).not.toHaveTextContent("Last activity");
    expect(screen.getByRole("button", { name: "Restart these workers" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Restart all workers" })).toBeEnabled();
  });

  it("restarts only the stalled workers' components, each once", async () => {
    post.mockResolvedValue({ restarted: 2, components: ["transcription"] });
    show(pulse(stalled()));
    await userEvent.click(await screen.findByRole("button", { name: "Restart these workers" }));
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0]).toBe("/api/queues/workers/restart");
    expect(post.mock.calls[0][1]).toEqual({ components: ["transcription"] });
    expect(await screen.findByRole("status")).toHaveTextContent("Restarting the workers.");
    expect(screen.queryByRole("button", { name: "Restart these workers" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Restart all workers" })).toBeNull();
  });

  it("restarts every worker with no component named", async () => {
    post.mockResolvedValue({ restarted: 4, components: [] });
    show(pulse(stalled()));
    await userEvent.click(await screen.findByRole("button", { name: "Restart all workers" }));
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0]).toBe("/api/queues/workers/restart");
    expect(post.mock.calls[0][1]).toBeUndefined();
    expect(await screen.findByRole("status")).toHaveTextContent("Restarting the workers.");
  });

  it("says why when the API refuses, and keeps the buttons", async () => {
    post.mockRejectedValue(
      new ApiError({ type: "about:blank", title: "the workers cannot be restarted", status: 503 })
    );
    show(pulse(stalled()));
    await userEvent.click(await screen.findByRole("button", { name: "Restart these workers" }));
    await waitFor(async () =>
      expect(await notice()).toHaveTextContent(
        "The workers could not be restarted: the workers cannot be restarted"
      )
    );
    expect(screen.getByRole("button", { name: "Restart these workers" })).toBeEnabled();
  });

  it("a worker on a joined machine is named by its machine and is not restarted from here", async () => {
    show(
      pulse(
        stalled({
          stalled: [
            worker({
              worker_id: "pc/9/stt",
              host: "pc",
              node_name: "Studio PC",
              restartable: false,
            }),
          ],
        })
      )
    );
    const alert = await notice();
    expect(alert).toHaveTextContent(/Transcription worker on .*Studio PC/);
    expect(alert).toHaveTextContent("A worker on another machine is restarted on that machine.");
    expect(screen.queryByRole("button", { name: "Restart these workers" })).toBeNull();
    expect(screen.getByRole("button", { name: "Restart all workers" })).toBeEnabled();
  });

  it("a server under no supervisor: no button, and what to do instead", async () => {
    show(pulse(stalled({ can_restart: false, stalled: [worker({ restartable: false })] })));
    const alert = await notice();
    expect(alert).toHaveTextContent("This server cannot restart its workers from here.");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("points to the jobs that failed meanwhile", async () => {
    show(pulse(stalled()));
    const alert = await notice();
    expect(alert).toHaveTextContent("are under Needs attention on this page: retry them there.");
    expect(within(alert).getByRole("link", { name: "Failing items" })).toHaveAttribute(
      "href",
      "/queue/failures"
    );
  });

  it("no worker registered at all: says so, with one waiting job and the restart-all button", async () => {
    show(pulse(stalled({ reason: "no_heartbeat", stalled: [], waiting: 1 })));
    const alert = (await screen.findByText("No worker is running.")).closest(
      ".mkt-q-pulse"
    ) as HTMLElement;
    expect(alert).toHaveTextContent("Jobs are waiting and no worker has reported in.");
    expect(alert).toHaveTextContent("One job is waiting for them. It stays queued");
    expect(within(alert).queryAllByRole("listitem")).toHaveLength(0);
    expect(screen.queryByRole("button", { name: "Restart these workers" })).toBeNull();
    expect(screen.getByRole("button", { name: "Restart all workers" })).toBeEnabled();
  });

  it("keeps the list of jobs that stopped beating under the notice", async () => {
    show(
      pulse(stalled(), {
        stuck: [
          {
            job_id: 9,
            stage: "probe",
            label: "حلقة 12",
            subject_kind: "video",
            subject_id: "v-1",
            heartbeat_age_sec: 1800,
            claimed_by: "pc/1",
            stalled_after_sec: 120,
            running_for_sec: 2000,
          },
        ],
      })
    );
    const alert = await notice();
    expect(alert).toHaveTextContent("probe: no heartbeat for 30 min");
  });
});

describe("the machine is overloaded", () => {
  it("says so with the load, the cores and the programs using the most CPU", async () => {
    show(pulse(running, { host: overloaded }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("This machine is overloaded.");
    expect(alert).toHaveTextContent(
      "Load 512.3 on 28 cores (the last minute; 498 over five minutes). Jobs and the database are slow until it eases."
    );
    expect(alert).toHaveTextContent(
      /Using the most CPU: .*runaway.* \(795%\), .*Some App.* \(398%\)\./
    );
    expect(alert).toHaveTextContent("Maktaba stops no program by itself.");
    // it explains only: nothing here ends a program
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText("Last activity 12 s ago")).toBeInTheDocument();
  });

  it("without a list of programs it says only that", async () => {
    show(pulse(running, { host: { ...overloaded, top: null } }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("This machine is overloaded.");
    expect(alert).not.toHaveTextContent("Using the most CPU");
    expect(alert).not.toHaveTextContent(EN["queue.hostOverloaded.hint"]);
  });

  it("a machine within its cores, or a pulse with no host block: no notice", async () => {
    const first = show(pulse(running, { host: { ...overloaded, overloaded: false, top: [] } }));
    expect(await screen.findByText("Last activity 12 s ago")).toBeInTheDocument();
    expect(screen.queryByText(EN["queue.hostOverloaded.title"])).toBeNull();
    first.unmount();
    show(pulse(running));
    expect(await screen.findByText("Last activity 12 s ago")).toBeInTheDocument();
    expect(screen.queryByText(EN["queue.hostOverloaded.title"])).toBeNull();
  });

  it("shows beside the stalled workers' notice when both hold", async () => {
    show(pulse(stalled(), { host: overloaded }));
    expect(await screen.findByText(EN["queue.hostOverloaded.title"])).toBeInTheDocument();
    expect(await screen.findByText(EN["queue.workersStalled.title"])).toBeInTheDocument();
    expect(screen.getAllByRole("alert")).toHaveLength(2);
  });
});

describe("in Arabic", () => {
  beforeEach(() => localStorage.setItem("mkt:locale", "ar"));

  it("the stalled workers, their names and the buttons are Arabic", async () => {
    show(
      pulse(
        stalled({
          stalled: [worker(), worker({ worker_id: "m", kind: "main", reason: "no_heartbeat" })],
        })
      )
    );
    const alert = (await screen.findByText(AR["queue.workersStalled.title"])).closest(
      ".mkt-q-pulse"
    ) as HTMLElement;
    expect(alert).toHaveTextContent(AR["queue.workersStalled.worker.transcription"]);
    expect(alert).toHaveTextContent(AR["queue.workersStalled.worker.main"]);
    expect(alert).toHaveTextContent("لم يبدأ أي مهمة");
    expect(alert).toHaveTextContent("لم يُبلّغ عن حاله منذ");
    expect(alert).toHaveTextContent("مهمة تنتظرهم");
    expect(alert).toHaveTextContent(AR["queue.workersStalled.failedJobs"]);
    expect(alert).not.toHaveTextContent(/workers|Restart|waiting/);
    expect(screen.getByRole("button", { name: AR["queue.workersStalled.restart"] })).toBeEnabled();
    expect(
      screen.getByRole("button", { name: AR["queue.workersStalled.restartAll"] })
    ).toBeEnabled();
  });

  it("the overloaded machine is said in Arabic", async () => {
    show(pulse(running, { host: overloaded }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(AR["queue.hostOverloaded.title"]);
    expect(alert).toHaveTextContent(AR["queue.hostOverloaded.hint"]);
    expect(alert).toHaveTextContent("runaway");
    expect(alert).not.toHaveTextContent(/overloaded|Load|cores/);
  });

  it("the workers run the jobs: no notice", async () => {
    show(pulse(running));
    await waitFor(() => expect(document.querySelector(".mkt-q-pulse")).not.toBeNull());
    expect(screen.queryByText(AR["queue.workersStalled.title"])).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("the strings", () => {
  it("ship in English and Arabic together", () => {
    for (const prefix of ["queue.workersStalled.", "queue.hostOverloaded."]) {
      const keys = Object.keys(EN).filter((k) => k.startsWith(prefix));
      expect(keys.length).toBeGreaterThan(4);
      for (const k of keys) {
        expect(AR[k], k).toBeTruthy();
        if (!k.endsWith(".proc")) expect(AR[k], k).not.toBe(EN[k]);
      }
      expect(
        Object.keys(AR)
          .filter((k) => k.startsWith(prefix))
          .sort()
      ).toEqual(keys.sort());
    }
  });
});
