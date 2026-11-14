// The Queues page says when the workers run no job, and lets an admin restart them
// (the pulse's `workers` block; POST /api/queues/workers/restart). States: fine, stalled
// for an admin, stalled for a user who is not one, in English and Arabic.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, isAdmin } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  isAdmin: { value: true },
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("./useIsAdmin", () => ({ useIsAdmin: () => isAdmin.value }));

import { ApiError } from "../../lib/api";
import type { Pulse, WorkersState } from "../../lib/queueApi";
import { PulseBanner } from "./PulseBanner";
import { renderAt, routeGet } from "../../test/queueHarness";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;

const stalled = (over: Partial<WorkersState> = {}): WorkersState => ({
  state: "not_responding",
  reason: "no_heartbeat",
  since: new Date(Date.now() - 3 * 3600_000).toISOString(),
  last_seen_at: new Date(Date.now() - 3 * 3600_000).toISOString(),
  waiting: 12,
  can_restart: true,
  ...over,
});
const pulse = (workers?: WorkersState, over: Partial<Pulse> = {}): Pulse => ({
  state: workers?.state === "not_responding" ? "stalled" : "working",
  running: workers?.state === "not_responding" ? 0 : 1,
  waiting: 12,
  paused_all: false,
  last_activity_at: "2026-05-18T11:59:48Z",
  age_sec: 12,
  stuck: [],
  workers,
  ...over,
});
const show = (p: Pulse) => {
  routeGet(get, { "/api/queues/pulse": p });
  return renderAt(<PulseBanner />, "/queue");
};

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  isAdmin.value = true;
});
afterEach(() => {
  localStorage.removeItem("mkt:locale");
  document.documentElement.dir = "ltr";
});

describe("the workers run the jobs", () => {
  it("shows the pulse line and no notice", async () => {
    show(pulse({ ...stalled(), state: "running", reason: undefined, since: null }));
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

describe("the workers are not running jobs: an admin", () => {
  it("says so, since when and what happens, with the restart button", async () => {
    show(pulse(stalled()));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The workers are not running jobs.");
    expect(alert).toHaveTextContent(/Since .+ \(3 h ago\)\./);
    expect(alert).toHaveTextContent("No worker has reported in since then.");
    expect(alert).toHaveTextContent(
      "12 jobs are waiting. They stay queued and start when the workers run again; nothing is lost."
    );
    expect(alert).not.toHaveTextContent("Last activity");
    expect(alert).not.toHaveTextContent(EN["queue.workersStalled.askAdmin"]);
    expect(screen.getByRole("button", { name: "Restart the workers" })).toBeEnabled();
  });

  it("names workers that report in and start nothing, and one waiting job", async () => {
    show(pulse(stalled({ reason: "no_dispatch", waiting: 1 })));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The workers report in but have started no job since then.");
    expect(alert).toHaveTextContent("One job is waiting. It stays queued");
  });

  it("restarts the workers through the API and says it asked", async () => {
    post.mockResolvedValue({ restarted: 2 });
    show(pulse(stalled()));
    await userEvent.click(await screen.findByRole("button", { name: "Restart the workers" }));
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0]).toBe("/api/queues/workers/restart");
    expect(await screen.findByRole("status")).toHaveTextContent("Restarting the workers.");
    expect(screen.queryByRole("button", { name: "Restart the workers" })).toBeNull();
  });

  it("says why when the API refuses, and keeps the button", async () => {
    post.mockRejectedValue(
      new ApiError({ type: "about:blank", title: "the workers cannot be restarted", status: 503 })
    );
    show(pulse(stalled()));
    await userEvent.click(await screen.findByRole("button", { name: "Restart the workers" }));
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "The workers could not be restarted: the workers cannot be restarted"
      )
    );
    expect(screen.getByRole("button", { name: "Restart the workers" })).toBeEnabled();
  });

  it("a server under no supervisor: no button, and what to do instead", async () => {
    show(pulse(stalled({ can_restart: false })));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("This server cannot restart its workers from here.");
    expect(screen.queryByRole("button")).toBeNull();
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
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The workers are not running jobs.");
    expect(alert).toHaveTextContent("probe: no heartbeat for 30 min");
  });
});

describe("the workers are not running jobs: not an admin", () => {
  it("shows the notice without the button and says whom to ask", async () => {
    isAdmin.value = false;
    show(pulse(stalled()));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The workers are not running jobs.");
    expect(alert).toHaveTextContent("12 jobs are waiting.");
    expect(alert).toHaveTextContent("Ask an admin to restart the workers.");
    expect(alert).not.toHaveTextContent(EN["queue.workersStalled.adminHint"]);
    expect(screen.queryByRole("button")).toBeNull();
    expect(post).not.toHaveBeenCalled();
  });
});

describe("in Arabic", () => {
  beforeEach(() => localStorage.setItem("mkt:locale", "ar"));

  it("an admin reads the notice and the button in Arabic", async () => {
    show(pulse(stalled()));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(AR["queue.workersStalled.title"]);
    expect(alert).toHaveTextContent(AR["queue.workersStalled.noHeartbeat"]);
    expect(alert).toHaveTextContent("مهمة تنتظر");
    expect(alert).not.toHaveTextContent("The workers");
    expect(
      screen.getByRole("button", { name: AR["queue.workersStalled.restart"] })
    ).toBeInTheDocument();
  });

  it("a user who is not an admin reads whom to ask, with no button", async () => {
    isAdmin.value = false;
    show(pulse(stalled({ reason: "no_dispatch", waiting: 2 })));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(AR["queue.workersStalled.noDispatch"]);
    expect(alert).toHaveTextContent(AR["queue.workersStalled.waiting.two"]);
    expect(alert).toHaveTextContent(AR["queue.workersStalled.askAdmin"]);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("the workers run the jobs: no notice", async () => {
    show(pulse({ ...stalled(), state: "running", reason: undefined, since: null }));
    await waitFor(() => expect(document.querySelector(".mkt-q-pulse")).not.toBeNull());
    expect(screen.queryByText(AR["queue.workersStalled.title"])).toBeNull();
  });
});

describe("the strings", () => {
  it("ship in English and Arabic together", () => {
    const keys = Object.keys(EN).filter((k) => k.startsWith("queue.workersStalled."));
    expect(keys.length).toBe(16);
    for (const k of keys) {
      expect(AR[k], k).toBeTruthy();
      expect(AR[k], k).not.toBe(EN[k]);
    }
    expect(
      Object.keys(AR)
        .filter((k) => k.startsWith("queue.workersStalled."))
        .sort()
    ).toEqual(keys.sort());
  });
});
