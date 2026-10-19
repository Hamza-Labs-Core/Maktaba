// The Processing pages filtered by machine (?host=<node id | home>), the
// target of the Machines page's "View in queues": the overview lists the
// machine's running jobs with their progress, the stage page narrows its
// running list to the machine.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
vi.mock("../../lib/ws", () => ({ subscribe: () => () => {} }));

import { QueueOverview } from "./QueueOverview";
import { QueueDetail } from "./QueueDetail";
import { renderAt, routeGet } from "../../test/queueHarness";
import {
  detailFixture,
  extensionsFixture,
  jobRef,
  librariesFixture,
  rateFixture,
  snapshotFixture,
} from "../../test/fixtures/queues";
import { HOST_HOME, hostWorkerIds, runsOnHost } from "../../components/queue/hostFilter";
import type { WorkerHost } from "../../lib/queueApi";

const NOW = new Date().toISOString();
const workers: WorkerHost[] = [
  {
    worker_id: "pc-1",
    host: "This PC",
    online: true,
    last_seen_at: NOW,
    serves: ["probe"],
    serves_label: "NAS",
    node_id: null,
  },
  {
    worker_id: "mac-1",
    host: "studio",
    online: true,
    last_seen_at: NOW,
    serves: ["probe"],
    serves_label: "transcription",
    node_id: "n1",
    node_name: "Studio Mac",
  },
  {
    worker_id: "mac-2",
    host: "studio",
    online: true,
    last_seen_at: NOW,
    serves: ["probe"],
    serves_label: "transcription",
    node_id: "n1",
    node_name: "Studio Mac",
  },
];

describe("host filter helpers", () => {
  it("finds a machine's workers: the home's own, or a joined machine's", () => {
    expect([...hostWorkerIds(workers, HOST_HOME)]).toEqual(["pc-1"]);
    expect([...hostWorkerIds(workers, "n1")]).toEqual(["mac-1", "mac-2"]);
    expect(hostWorkerIds(null, "n1").size).toBe(0);
  });

  it("places a job by its claimed_by, else by the hub's live run", () => {
    const ids = hostWorkerIds(workers, "n1");
    expect(runsOnHost({ claimed_by: "mac-2" }, undefined, ids)).toBe(true);
    expect(runsOnHost({ claimed_by: "pc-1" }, { worker_id: "mac-1" }, ids)).toBe(false);
    expect(runsOnHost({}, { worker_id: "mac-1" }, ids)).toBe(true);
    expect(runsOnHost({ claimed_by: null }, undefined, ids)).toBe(false);
  });
});

describe("Processing pages with ?host=", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockReset();
  });

  it("lists the machine's running jobs with their progress on the overview", async () => {
    routeGet(get, {
      "/api/queues": snapshotFixture,
      "/api/issues": { groups: [] },
      "/api/queues/libraries": { items: librariesFixture },
      "/api/queues/health": { series: [] },
      "/api/workers": { workers },
      "/api/extensions": { extensions: extensionsFixture },
      "/api/activity": { items: [], next: null },
      "/api/queues/running": (url: string) => ({
        host: "n1",
        jobs: url.includes("host=n1")
          ? [
              jobRef({
                id: 7,
                subject_label: "Lecture 4",
                claimed_by: "mac-1",
                progress: {
                  step: "transcribe",
                  steps: ["transcribe"],
                  done_units: 1,
                  total_units: 4,
                },
              }),
            ]
          : [],
      }),
    });
    renderAt(<QueueOverview />, "/queue?host=n1", "/queue");
    const panel = (await screen.findByRole("heading", { name: /Running on/ })).closest("section")!;
    expect(get).toHaveBeenCalledWith("/api/queues/running?host=n1");
    expect(await within(panel).findByText(/Lecture 4/)).toBeInTheDocument();
    expect(
      within(panel).getByRole("progressbar", { name: "Progress of Lecture 4" })
    ).toHaveAttribute("aria-valuenow", "25");
    expect(await within(panel).findByText(/Studio Mac/)).toBeInTheDocument();
    expect(within(panel).getByRole("link", { name: "Show every machine" })).toHaveAttribute(
      "href",
      "/queue"
    );
  });

  it("titles the panel with the name the link carried until the workers know it", async () => {
    routeGet(get, {
      "/api/queues": snapshotFixture,
      "/api/issues": { groups: [] },
      "/api/queues/libraries": { items: [] },
      "/api/queues/health": { series: [] },
      "/api/workers": { workers: [] },
      "/api/extensions": { extensions: [] },
      "/api/activity": { items: [], next: null },
      "/api/queues/running": { host: "n9", jobs: [] },
    });
    renderAt(<QueueOverview />, "/queue?host=n9&name=Spare%20box", "/queue");
    expect(
      await screen.findByRole("heading", { name: /Running on .*Spare box/ })
    ).toBeInTheDocument();
    expect(await screen.findByText(/Nothing is running on .*Spare box/)).toBeInTheDocument();
  });

  it("has no machine panel without ?host=", async () => {
    routeGet(get, {
      "/api/queues": snapshotFixture,
      "/api/issues": { groups: [] },
      "/api/queues/libraries": { items: [] },
      "/api/queues/health": { series: [] },
      "/api/workers": { workers },
      "/api/extensions": { extensions: [] },
      "/api/activity": { items: [], next: null },
    });
    renderAt(<QueueOverview />, "/queue", "/queue");
    await screen.findAllByRole("heading", { level: 1 });
    expect(screen.queryByRole("heading", { name: /Running on/ })).toBeNull();
    expect(get).not.toHaveBeenCalledWith(expect.stringContaining("/api/queues/running"));
  });

  it("narrows a stage's running jobs to the machine", async () => {
    const detail = detailFixture("probe");
    detail.running = [
      jobRef({ id: 1, stage: "probe", subject_label: "On the Mac", claimed_by: "mac-2" }),
      jobRef({ id: 2, stage: "probe", subject_label: "On the PC", claimed_by: "pc-1" }),
    ];
    routeGet(get, {
      "/api/queues": snapshotFixture,
      "/api/queues/probe": detail,
      "/api/queues/probe/rate": rateFixture,
      "/api/queues/libraries": { items: [] },
      "/api/workers": { workers },
      "/api/activity": { items: [], next: null },
    });
    renderAt(<QueueDetail />, "/queue/probe?host=n1", "/queue/:stage");
    const running = (await screen.findByRole("heading", { name: "Running now" })).closest(
      "section"
    )!;
    expect(await within(running).findByText(/Only the jobs on Studio Mac/)).toBeInTheDocument();
    expect(within(running).getByText(/On the Mac/)).toBeInTheDocument();
    expect(within(running).queryByText(/On the PC/)).toBeNull();
    expect(within(running).getByRole("link", { name: "Show every machine" })).toHaveAttribute(
      "href",
      "/queue/probe"
    );
  });
});
