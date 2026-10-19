// Cases from web/test-cases/queue-reports.md (QRP-*), rendered through the
// shared page harness (RequireAuth + route error boundary + signed-in user).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route } from "react-router-dom";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { ApiError } from "../../lib/api";
import type { PlanPreviewStatus, Report } from "../../lib/reportsApi";
import { Reports } from "./Reports";
import { routeGet } from "../../test/queueHarness";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";

const report = (over: Partial<Report> & { name: string }): Report => ({
  kind: "other",
  title: over.name,
  library_id: null,
  size: 10,
  updated_at: "2026-05-18T11:00:00Z",
  ...over,
});

const reports: Report[] = [
  report({ name: "channel-plan.html", kind: "plan", title: "Channel plan" }),
  report({ name: "channel-audit.html", kind: "audit", title: "Channel audit" }),
  report({ name: "lib-1.html", kind: "review", title: "قناة الاختبار", library_id: "lib-1" }),
  report({ name: "channel-plan-preview.html", kind: "preview", title: "Channel plan (preview)" }),
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

type Table = Record<string, unknown>;

function setup(
  over: Table = {},
  opts: { path?: string; user?: typeof memberUser | null; history?: string[] } = {}
) {
  routeGet(get, {
    "/api/reports": { reports },
    "/api/reports/channel-plan/preview": idle,
    ...over,
  });
  return renderPage(<Reports />, {
    path: opts.path ?? "/queue/reports",
    route: "/queue/reports",
    user: opts.user,
    history: opts.history,
    extraRoutes: <Route path="/queue" element={<p>overview page</p>} />,
  });
}

const calls = (path: string) => get.mock.calls.filter((c) => String(c[0]).split("?")[0] === path);
const noCrash = () =>
  expect(screen.queryByText("This page hit an error and could not be shown.")).toBeNull();
const listNav = () => screen.findByRole("navigation", { name: "Report pages" });

describe("Reports (test-cases/queue-reports.md)", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset().mockResolvedValue({ started: true, kind: "preview" });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("QRP-05: path-like names stay encoded inside /api/reports/", async () => {
    const a = setup(
      { "/api/reports": { reports: [...reports, report({ name: "../secret.html" })] } },
      { path: "/queue/reports?open=..%2Fsecret.html" }
    );
    const frame = await screen.findByTitle("../secret.html");
    expect(frame.getAttribute("src")).toMatch(/\/api\/reports\/\.\.%2Fsecret\.html$/);
    expect(frame.getAttribute("src")).not.toContain("/api/secret");
    a.unmount();
    const b = setup({}, { path: "/queue/reports?open=..%2Fsecret.html" });
    expect(
      await screen.findByText("The report ../secret.html does not exist (any more).")
    ).toBeInTheDocument();
    expect(document.querySelector("iframe")).toBeNull();
    b.unmount();
    setup({}, { path: "/queue/reports?open=javascript:alert(1)" });
    expect(
      await screen.findByText("The report javascript:alert(1) does not exist (any more).")
    ).toBeInTheDocument();
    expect(document.querySelector("iframe")).toBeNull();
    expect(document.querySelector('a[href^="javascript:"]')).toBeNull();
  });

  it("QRP-06: shows Loading in the list while it is pending", async () => {
    setup({ "/api/reports": new Promise(() => {}) });
    const nav = await listNav();
    expect(within(nav).getByText("Loading…")).toBeInTheDocument();
    expect(within(nav).getByRole("searchbox", { name: "Filter reports" })).toBeInTheDocument();
    expect(screen.queryByText(/No reports yet/)).toBeNull();
    expect(await screen.findByRole("heading", { name: "Preview channels" })).toBeInTheDocument();
  });

  it("QRP-08: 401 / 403 / truncated JSON / HTML list response show the load error", async () => {
    const variants: unknown[] = [
      () =>
        Promise.reject(new ApiError({ type: "about:blank", title: "Unauthorized", status: 401 })),
      () => Promise.reject(new ApiError({ type: "about:blank", title: "Forbidden", status: 403 })),
      () => Promise.reject(new SyntaxError("Unexpected end of JSON input")),
      "<!doctype html><html></html>",
    ];
    for (const v of variants) {
      const { unmount } = setup({ "/api/reports": v });
      expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the reports.");
      noCrash();
      unmount();
    }
  });

  it("QRP-09: reports null or missing shows the empty folder text", async () => {
    for (const body of [{ reports: null }, {}]) {
      const a = setup({ "/api/reports": body });
      expect(await screen.findByText(/^No reports yet\./)).toBeInTheDocument();
      noCrash();
      a.unmount();
      const b = setup({ "/api/reports": body }, { path: "/queue/reports?open=channel-plan.html" });
      expect(
        await screen.findByText("The report channel-plan.html does not exist (any more).")
      ).toBeInTheDocument();
      b.unmount();
    }
  });

  it("QRP-10: preview status with summary null / partial renders the panel", async () => {
    const variants: PlanPreviewStatus[] = [
      { ...idle, summary: null as unknown as PlanPreviewStatus["summary"] },
      { ...idle, summary: { channels: 3, lanes: 4, added: null as unknown as string[] } },
      { ...idle, summary: { channels: 3, lanes: 4 } },
    ];
    for (const s of variants) {
      const { unmount } = setup({ "/api/reports/channel-plan/preview": s });
      expect(await screen.findByRole("button", { name: "Preview channels" })).toBeInTheDocument();
      noCrash();
      unmount();
    }
  });

  it.each([
    ["log null", { ...idle, log: null }],
    ["log missing", { ...idle, log: undefined }],
    ["ready null", { ...idle, ready: null }],
    ["ready missing", { ...idle, ready: undefined }],
  ])("QRP-10: preview status %s does not crash (PlanPreviewPanel.tsx)", async (_n, s) => {
    quietCrash();
    setup({ "/api/reports/channel-plan/preview": s });
    expect(await screen.findByRole("button", { name: "Preview channels" })).toBeInTheDocument();
    await waitFor(() => expect(calls("/api/reports/channel-plan/preview").length).toBe(1));
    await new Promise((r) => setTimeout(r, 20));
    noCrash();
  });

  it("QRP-11: a filter with no match shows no groups; clearing brings them back", async () => {
    setup();
    const nav = await listNav();
    await within(nav).findByRole("button", { name: /Channel audit/ });
    const filter = within(nav).getByRole("searchbox", { name: "Filter reports" });
    await userEvent.type(filter, "zzz");
    expect(within(nav).queryAllByRole("heading")).toHaveLength(0);
    await userEvent.clear(filter);
    expect(within(nav).getAllByRole("heading").length).toBe(4);
    await userEvent.type(filter, "AUDIT");
    expect(within(nav).getByRole("button", { name: /Channel audit/ })).toBeInTheDocument();
    expect(within(nav).queryByRole("button", { name: /Channel plan\u2069/ })).toBeNull();
  });

  it("QRP-12: render crash shows the route error fallback; Back works", async () => {
    quietCrash();
    routeGet(get, {
      "/api/reports": { reports: [null] },
      "/api/reports/channel-plan/preview": idle,
    });
    renderPage(<Reports />, { path: "/queue/reports" });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await userEvent.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routeGet(get, { "/api/reports": { reports }, "/api/reports/channel-plan/preview": idle });
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Reports" })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /Channel audit/ })).toBeInTheDocument();
  });

  it("QRP-13: signed-out user is sent to /login; no report is loaded", async () => {
    setup({}, { path: "/queue/reports?open=channel-plan.html", user: null });
    expect(
      await screen.findByText("login page (from /queue/reports?open=channel-plan.html)")
    ).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("QRP-14: non-admin sees reports and the preview link but no preview buttons", async () => {
    setup({}, { user: memberUser });
    await listNav();
    expect(await screen.findByRole("link", { name: "Open the preview" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Preview channels" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Show the stored plan" })).toBeNull();
    await userEvent.click(await screen.findByRole("button", { name: /Channel audit/ }));
    expect(await screen.findByTitle("Channel audit")).toBeInTheDocument();
  });

  it("QRP-18: picking replaces the entry (Back leaves); Open the preview pushes", async () => {
    setup({}, { history: ["/queue"] });
    const nav = await listNav();
    await userEvent.click(await within(nav).findByRole("button", { name: /Channel plan\u2069/ }));
    await userEvent.click(within(nav).getByRole("button", { name: /Channel audit/ }));
    expect(await screen.findByTitle("Channel audit")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("link", { name: "Open the preview" }));
    expect(await screen.findByTitle("Channel plan (preview)")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByTitle("Channel audit")).toBeInTheDocument();
    // picking uses replace: one more Back leaves the page (not back to the plan)
    await userEvent.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("overview page")).toBeInTheDocument();
  });
});
