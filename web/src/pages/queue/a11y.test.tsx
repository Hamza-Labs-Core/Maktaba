// Automated accessibility check (axe-core) of the queue overview, a queue page,
// the item page and the Extensions admin page, rendered from the fixtures.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";

const { get, post, put, patch } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch } };
});
vi.mock("../../lib/ws", () => ({ subscribe: () => () => undefined }));

import { QueueOverview } from "./QueueOverview";
import { QueueDetail } from "./QueueDetail";
import { Reports } from "./Reports";
import { ItemHistory } from "./ItemHistory";
import { AdminExtensions } from "../Admin/Extensions";
import { renderAt, routeGet } from "../../test/queueHarness";
import { axeViolations } from "../../test/axe";
import {
  detailFixture,
  extensionsFixture,
  failureGroupsFixture,
  healthSeriesFixture,
  libraryHistoryFixture,
  librariesFixture,
  logFixture,
  rateFixture,
  snapshotFixture,
  workersFixture,
} from "../../test/fixtures/queues";

const table = {
  "/api/queues": snapshotFixture,
  "/api/issues": { groups: failureGroupsFixture },
  "/api/queues/libraries": { items: librariesFixture },
  "/api/queues/health": { series: healthSeriesFixture },
  "/api/workers": { workers: workersFixture },
  "/api/extensions": { extensions: extensionsFixture },
  "/api/activity": { items: logFixture, next: null },
  "/api/queues/probe": detailFixture("probe"),
  "/api/queues/probe/rate": rateFixture,
  "/api/items/library/lib-1/history": libraryHistoryFixture,
  "/api/queues/pulse": {
    state: "stalled",
    running: 1,
    waiting: 3,
    paused_all: false,
    last_activity_at: null,
    age_sec: 900,
    stuck: [
      {
        job_id: 9,
        stage: "probe",
        label: "Lecture 04",
        subject_kind: "video",
        subject_id: "v-1",
        heartbeat_age_sec: 600,
        claimed_by: "pc/1",
        stalled_after_sec: 120,
        running_for_sec: 700,
      },
    ],
  },
  "/api/queues/insights": {
    totals: {
      libraries: 2,
      videos: 321,
      videos_with_length: 300,
      series: 5,
      seasons: 9,
      plex_collections: 4,
      dizquetv_shows: 1,
      channels: 3,
      style_pinned: 1,
      youtube_videos: 0,
      videos_without_exact_date: 0,
    },
    styles: { talk: 6, cinematic: 2 },
    genres: [{ genre: "fiqh", seasons: 4 }],
  },
  "/api/reports": {
    reports: [
      {
        name: "channel-plan.html",
        kind: "plan",
        title: "Channel plan",
        library_id: null,
        size: 1,
        updated_at: "2026-05-18T11:00:00Z",
      },
      {
        name: "lib-1.html",
        kind: "review",
        title: "Islamic lectures",
        library_id: "lib-1",
        size: 1,
        updated_at: "2026-05-18T11:00:00Z",
      },
    ],
  },
  "/api/reports/channel-plan/preview": {
    running: false,
    kind: "preview",
    phase: "",
    started_at: 1,
    finished_at: 2,
    log: ["done"],
    summary: {
      channels: 3,
      lanes: 5,
      changes: { added: 1, removed: 0, changed: 1, reordered: 0 },
      added: ["Mix"],
    },
    ready: { preview: true, plan: true },
  },
};

describe("accessibility (axe)", () => {
  beforeEach(() => {
    localStorage.clear();
    get.mockReset();
    post.mockResolvedValue(undefined);
    routeGet(get, table);
  });

  it("queue overview has no violations", async () => {
    const { container } = renderAt(<QueueOverview />, "/queue");
    await screen.findByRole("link", { name: "Length" });
    expect(await axeViolations(container)).toEqual([]);
  });

  it("reports page has no violations", async () => {
    const { container } = renderAt(<Reports />, "/queue/reports");
    await screen.findByRole("button", { name: /Islamic lectures/ }); // the frame itself is the server's page
    expect(await axeViolations(container)).toEqual([]);
  });

  it("queue page has no violations", async () => {
    const { container } = renderAt(<QueueDetail />, "/queue/probe", "/queue/:stage");
    await screen.findByRole("heading", { level: 1 });
    expect(await axeViolations(container)).toEqual([]);
  });

  it("item page has no violations", async () => {
    const { container } = renderAt(
      <ItemHistory />,
      "/queue/items/library/lib-1",
      "/queue/items/:kind/:id"
    );
    await screen.findByRole("heading", { level: 1, name: /Islamic lectures/ });
    expect(await axeViolations(container)).toEqual([]);
  });

  it("extensions page has no violations", async () => {
    const { container } = renderAt(<AdminExtensions />, "/admin/extensions");
    await screen.findByRole("heading", { name: "Plex" });
    expect(await axeViolations(container)).toEqual([]);
  });
});
