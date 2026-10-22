import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../../lib/api";
import type { PlanPreviewStatus, Report } from "../../lib/reportsApi";
import { Reports } from "./Reports";
import { renderAt, routeGet } from "../../test/queueHarness";

const LIB = "0a1b2c3d-0000-4000-8000-00000000abcd";
const reports: Report[] = [
  {
    name: "channel-plan.html",
    kind: "plan",
    title: "Channel plan",
    library_id: null,
    size: 10,
    updated_at: "2026-05-18T11:00:00Z",
  },
  {
    name: "channel-audit.html",
    kind: "audit",
    title: "Channel audit",
    library_id: null,
    size: 10,
    updated_at: "2026-05-18T11:00:00Z",
  },
  {
    name: `${LIB}.html`,
    kind: "review",
    title: "قناة الاختبار",
    library_id: LIB,
    size: 10,
    updated_at: "2026-05-18T11:00:00Z",
  },
  {
    name: "channel-plan-preview.html",
    kind: "preview",
    title: "Channel plan (preview)",
    library_id: null,
    size: 10,
    updated_at: "2026-05-18T11:30:00Z",
  },
];

const idle: PlanPreviewStatus = {
  running: false,
  kind: null,
  phase: "",
  started_at: null,
  finished_at: null,
  summary: {},
  log: [],
  ready: { preview: true, plan: true },
};

function setup(path = "/queue/reports", status: PlanPreviewStatus = idle) {
  routeGet(get, {
    "/api/reports": { reports },
    "/api/reports/channel-plan/preview": () => status,
  });
  return renderAt(<Reports />, path, "/queue/reports");
}

describe("Reports", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset().mockResolvedValue({ started: true, kind: "preview" });
  });
  afterEach(() => vi.useRealTimers());

  it("groups the reports by kind, plan first, and names a review page after its library", async () => {
    setup();
    // the list is there (Loading…) before the reports answer: wait for its groups
    const nav = await screen.findByRole("navigation", { name: "Report pages" });
    const headings = (await within(nav).findAllByRole("heading")).map((h) => h.textContent);
    expect(headings).toEqual([
      "Channel plan",
      "Plan preview",
      "Channel audit",
      "Library review pages (1)",
    ]);
    expect(within(nav).getByRole("button", { name: /قناة الاختبار/ })).toBeInTheDocument();
    expect(screen.getByText("Pick a report to read it here.")).toBeInTheDocument();
  });

  it("opens a report in a frame that runs no script and has no same-site powers beyond cookies", async () => {
    setup();
    await userEvent.click(await screen.findByRole("button", { name: /Channel audit/ }));
    const frame = await screen.findByTitle("Channel audit");
    expect(frame).toHaveAttribute("sandbox", "allow-same-origin");
    expect(frame.getAttribute("sandbox")).not.toMatch(/allow-scripts/);
    expect(frame).toHaveAttribute("src", "/api/reports/channel-audit.html");
    expect(frame).toHaveAttribute("referrerpolicy", "no-referrer");
    expect(screen.getByRole("link", { name: "Open in a new tab" })).toHaveAttribute(
      "href",
      "/api/reports/channel-audit.html"
    );
  });

  it("opens the report named in the address, encoding the name", async () => {
    setup(`/queue/reports?open=${LIB}.html`);
    const frame = await screen.findByTitle("قناة الاختبار");
    expect(frame).toHaveAttribute("src", `/api/reports/${LIB}.html`);
  });

  it("says so when the report in the address is not there", async () => {
    setup("/queue/reports?open=gone.html");
    expect(
      await screen.findByText("The report gone.html does not exist (any more).")
    ).toBeInTheDocument();
    expect(screen.queryByTitle("gone.html")).toBeNull();
  });

  it("filters the list", async () => {
    setup();
    await userEvent.type(await screen.findByRole("searchbox", { name: "Filter reports" }), "audit");
    const nav = screen.getByRole("navigation", { name: "Report pages" });
    expect(within(nav).getAllByRole("button")).toHaveLength(1);
  });

  it("shows an empty folder as such", async () => {
    routeGet(get, {
      "/api/reports": { reports: [] },
      "/api/reports/channel-plan/preview": () => idle,
    });
    renderAt(<Reports />, "/queue/reports", "/queue/reports");
    expect(await screen.findByText(/No reports yet/)).toBeInTheDocument();
  });

  it("offers a retry when the list cannot be loaded", async () => {
    get.mockRejectedValue(new Error("down"));
    renderAt(<Reports />, "/queue/reports", "/queue/reports");
    expect(await screen.findByText(/Could not load the reports/)).toBeInTheDocument();
    routeGet(get, { "/api/reports": { reports }, "/api/reports/channel-plan/preview": () => idle });
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("button", { name: /قناة الاختبار/ })).toBeInTheDocument();
  });

  it("starts a preview, shows its progress and reloads the list and frame when it ends", async () => {
    let status: PlanPreviewStatus = idle;
    routeGet(get, {
      "/api/reports": () => ({ reports }),
      "/api/reports/channel-plan/preview": () => status,
    });
    renderAt(<Reports />, "/queue/reports?open=channel-plan-preview.html", "/queue/reports");
    await screen.findByTitle("Channel plan (preview)");
    const listCalls = () => get.mock.calls.filter((c) => c[0] === "/api/reports").length;
    const before = listCalls();

    status = { ...idle, running: true, kind: "preview", phase: "choosing the lineup", log: ["a"] };
    await userEvent.click(screen.getByRole("button", { name: "Preview channels" }));
    expect(post).toHaveBeenCalledWith("/api/reports/channel-plan/preview", { kind: "preview" });
    expect(await screen.findByText(/Planning: choosing the lineup/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview channels" })).toBeDisabled();

    status = {
      ...idle,
      summary: {
        channels: 12,
        lanes: 30,
        changes: { added: 1, removed: 0, changed: 2, reordered: 0 },
        added: ["قناة جديدة"],
      },
    };
    await waitFor(
      () => expect(screen.getByText(/12 channels and 30 slot queues planned/)).toBeInTheDocument(),
      {
        timeout: 5000,
      }
    );
    expect(screen.getByText(/1 channels would be added/)).toBeInTheDocument();
    expect(screen.getByText(/قناة جديدة/)).toBeInTheDocument();
    expect(listCalls()).toBeGreaterThan(before);
  });

  it("a second click while one runs says so (409)", async () => {
    setup();
    post.mockRejectedValue(
      new ApiError({
        type: "x",
        title: "conflict",
        status: 409,
        detail: "A preview is already being planned.",
      })
    );
    await userEvent.click(await screen.findByRole("button", { name: "Preview channels" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "A preview is already being planned."
    );
  });

  it("renders the stored plan on request", async () => {
    setup();
    post.mockResolvedValue({ started: true, kind: "stored" });
    await userEvent.click(await screen.findByRole("button", { name: "Show the stored plan" }));
    expect(post).toHaveBeenCalledWith("/api/reports/channel-plan/preview", { kind: "stored" });
  });

  it("shows a failed preview's reason and a dead pipeline", async () => {
    setup("/queue/reports", { ...idle, error: "There is no stored plan yet" });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The preview failed: There is no stored plan yet"
    );
  });

  it("tells when the pipeline does not answer", async () => {
    routeGet(get, { "/api/reports": { reports } });
    renderAt(<Reports />, "/queue/reports", "/queue/reports");
    expect(await screen.findByText(/The pipeline does not answer/)).toBeInTheDocument();
    await act(async () => {});
  });
});
