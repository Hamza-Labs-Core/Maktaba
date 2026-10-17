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
