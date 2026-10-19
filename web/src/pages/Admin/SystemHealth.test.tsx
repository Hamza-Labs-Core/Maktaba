// System health cases from web/test-cases/admin-health.md.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderPage, memberUser, quietCrash } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    api: { ...actual.api, get, post: vi.fn(), put: vi.fn(), patch: vi.fn(), delete: vi.fn() },
  };
});

import { ApiError } from "../../lib/api";
import { SystemHealth } from "./SystemHealth";

const H = "/api/system/health";
const CRASH = "This page hit an error and could not be shown.";
const SECTION_ERR = "Could not load this section.";

function table(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    [H]: {
      status: "ok",
      services: {
        db: { status: "up", checks: { ping: { status: "pass" } } },
        pipeline: { status: "degraded", reason: "queue slow" },
      },
      disk_free_bytes: 53687091200,
      queue_depth: 12,
      transcribe_budget_usd_left: "4.20",
    },
    "/api/system/version": {
      version: "1.5.0",
      build_sha: "abcdef1234567890",
      build_time: "2026-10-01T10:00:00Z",
      go_version: "go1.23",
      schema_revision: 124,
    },
    "/api/admin/caches": { caches: ["thumbs", "posters"] },
    "/api/admin/perf/budgets": { Version: 3, Endpoints: [{}, {}] },
    "/api/tv/cluster": {
      hosts: [{ state: "online" }, { state: "down" }],
      on_air: [{ stale: false }, { stale: true }],
      failovers_24h: 1,
    },
    "/api/tv/remote": {
      integration: { configured: true, enabled: true },
      remote: { ready: true },
    },
    "/api/system/processes": { available: true, processes: [], events: [] },
    ...over,
  };
}

const reject = (e: unknown) => () => Promise.reject(e);
const apiErr = (status: number) => new ApiError({ type: "about:blank", title: "x", status });

const open = () => renderPage(<SystemHealth />, { path: "/admin/health" });

/** The Card whose header or stat label is `label`. */
function card(label: string): HTMLElement {
  return screen.getByText(label, { selector: "strong" }).closest(".mk-card") as HTMLElement;
}

async function loaded() {
  await screen.findByRole("heading", { level: 1, name: "System health" });
  await waitFor(() => expect(screen.queryAllByText("Loading…")).toHaveLength(0));
}

describe("SystemHealth (test-cases/admin-health.md)", () => {
  beforeEach(() => {
    get.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("HLT-01: full dashboard renders when every endpoint answers", async () => {
    routeGet(get, table());
    open();
    await loaded();
    expect(screen.getByText("Auto-refreshes every 30s")).toBeInTheDocument();
    expect(within(card("Overall status")).getByText("Operational")).toBeInTheDocument();
    expect(within(card("Disk free")).getByText("50.0 GB")).toBeInTheDocument();
    expect(within(card("Queue depth")).getByText("12")).toBeInTheDocument();
    expect(within(card("Transcribe budget left")).getByText("$4.20")).toBeInTheDocument();
    const db = card("db");
    expect(within(db).getAllByText("Operational")).toHaveLength(2);
    expect(within(db).getByText("ping")).toBeInTheDocument();
    const pipe = card("pipeline");
    expect(within(pipe).getByText("Degraded")).toBeInTheDocument();
    expect(within(pipe).getByText("queue slow")).toBeInTheDocument();
    expect(await screen.findByTestId("health-tv")).toHaveTextContent(
      "TV machines online 1 of 2 · channels on air 1 · failovers (24 h) 1 · remote viewing ready"
    );
    const caches = card("Caches");
    expect(within(caches).getByText("thumbs")).toBeInTheDocument();
    expect(within(caches).getByText("posters")).toBeInTheDocument();
    const budgets = card("Performance budgets");
    expect(within(budgets).getByText("Budget version").nextSibling).toHaveTextContent("3");
    expect(within(budgets).getByText("Endpoint budgets").nextSibling).toHaveTextContent("2");
    const ver = screen.getAllByText("Version", { selector: "strong" })[0].closest(".mk-card")!;
    expect(within(ver as HTMLElement).getByText("1.5.0")).toBeInTheDocument();
    expect(within(ver as HTMLElement).getByText("abcdef123456")).toBeInTheDocument();
    expect(within(ver as HTMLElement).getByText("go1.23")).toBeInTheDocument();
    expect(within(ver as HTMLElement).getByText("124")).toBeInTheDocument();
    expect(card("Processes")).toBeInTheDocument();
  });

  it.each([
    ["is_admin false", memberUser],
    ["is_admin undefined", { id: "u2", username: "viewer" } as typeof memberUser],
  ])("HLT-02: non-admin (%s) sees Admins only and no calls", async (_, user) => {
    routeGet(get, table());
    renderPage(<SystemHealth />, { path: "/admin/health", user });
    expect(await screen.findByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "System health" })).toBeNull();
    expect(get).not.toHaveBeenCalled();
  });

  it("HLT-03: signed-out user is sent to /login", async () => {
    renderPage(<SystemHealth />, { path: "/admin/health", user: null });
    expect(await screen.findByText("login page (from /admin/health)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("HLT-04: status words map to Operational / Down / Degraded", async () => {
    const words = {
      ok: "Operational",
      UP: "Operational",
      healthy: "Operational",
      ready: "Operational",
      pass: "Operational",
      down: "Down",
      fail: "Down",
      error: "Down",
      unhealthy: "Down",
      degraded: "Degraded",
      starting: "Degraded",
      "": "Degraded",
    };
    const services = Object.fromEntries(
      Object.keys(words).map((w) => [`svc-${w || "empty"}`, { status: w }])
    );
    routeGet(get, table({ [H]: { status: "unknown", services } }));
    open();
    await loaded();
    expect(within(card("Overall status")).getByText("Degraded")).toBeInTheDocument();
    for (const [w, label] of Object.entries(words)) {
      const badge = within(card(`svc-${w || "empty"}`)).getByText(label);
      const tone = label === "Operational" ? "success" : label === "Down" ? "error" : "warn";
      expect(badge).toHaveClass(`mk-badge--${tone}`);
    }
  });

  it("HLT-05: loading state before health answers", async () => {
    routeGet(get, table({ [H]: () => new Promise(() => {}) }));
    open();
    await screen.findByRole("heading", { level: 1, name: "System health" });
    const services = screen.getByRole("heading", { level: 2, name: "Services" });
    expect(services.nextElementSibling).toHaveTextContent("Loading…");
    expect(within(card("Overall status")).getByText("Degraded")).toBeInTheDocument();
    expect(within(card("Disk free")).getByText("—")).toBeInTheDocument();
    expect(within(card("Queue depth")).getByText("—")).toBeInTheDocument();
    expect(await within(card("Caches")).findByText("thumbs")).toBeInTheDocument();
  });

  it.each([
    ["500", apiErr(500)],
    ["403", apiErr(403)],
    ["401", apiErr(401)],
    ["network", new TypeError("Failed to fetch")],
  ])("HLT-06: health fails (%s): overall Down, services section error", async (_, err) => {
    routeGet(get, table({ [H]: reject(err) }));
    open();
    expect(await screen.findByText(SECTION_ERR)).toBeInTheDocument();
    expect(within(card("Overall status")).getByText("Down")).toHaveClass("mk-badge--error");
    expect(within(card("Disk free")).getByText("—")).toBeInTheDocument();
    expect(within(card("Queue depth")).getByText("—")).toBeInTheDocument();
    expect(await within(card("Caches")).findByText("thumbs")).toBeInTheDocument();
  });

  it.each([
    ["/api/system/version", "Version"],
    ["/api/admin/caches", "Caches"],
    ["/api/admin/perf/budgets", "Performance budgets"],
    ["/api/system/processes", "Processes"],
  ])("HLT-07: %s failing shows the error in its own card only", async (path, label) => {
    routeGet(get, table({ [path]: reject(apiErr(500)) }));
    open();
    await screen.findByTestId("health-tv");
    await waitFor(() => expect(screen.getAllByText(SECTION_ERR)).toHaveLength(1));
    const errCard = screen.getByText(SECTION_ERR).closest(".mk-card");
    const target = screen.getAllByText(label, { selector: "strong" })[0].closest(".mk-card");
    expect(errCard).toBe(target);
    expect(card("db")).toBeInTheDocument();
  });

  it("HLT-07: tv/cluster failing hides the TV line; tv/remote failing drops the suffix", async () => {
    routeGet(get, table({ "/api/tv/cluster": reject(apiErr(500)) }));
    const { unmount } = open();
    await loaded();
    expect(screen.queryByTestId("health-tv")).toBeNull();
    expect(card("db")).toBeInTheDocument();
    unmount();
    routeGet(get, table({ "/api/tv/remote": reject(apiErr(500)) }));
    open();
    const line = await screen.findByTestId("health-tv");
    await loaded();
    expect(line).toHaveTextContent("failovers (24 h) 1");
    expect(line).not.toHaveTextContent("remote viewing");
  });

  it.each([
    ["null", { status: "ok", services: null }],
    ["missing", { status: "ok" }],
    ["empty", { status: "ok", services: {} }],
    ["checks null", { status: "ok", services: { db: { status: "ok", checks: null } } }],
  ])("HLT-08: services %s renders an empty Services section", async (_, body) => {
    routeGet(get, table({ [H]: body }));
    open();
    await loaded();
    expect(within(card("Overall status")).getByText("Operational")).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
    expect(screen.getByRole("heading", { level: 2, name: "Services" })).toBeInTheDocument();
  });

  it("HLT-09: service or check with missing status shows Degraded, no crash", async () => {
    routeGet(
      get,
      table({
        [H]: { status: null, services: { db: {}, api: { status: "ok", checks: { x: {} } } } },
      })
    );
    open();
    await loaded();
    expect(screen.queryByText(CRASH)).toBeNull();
    expect(within(card("db")).getByText("Degraded")).toBeInTheDocument();
    expect(within(card("api")).getByText("Degraded")).toBeInTheDocument();
    expect(within(card("Overall status")).getByText("Degraded")).toBeInTheDocument();
  });

  it.each([
    ["hosts null", { hosts: null, on_air: [] }],
    ["on_air null", { hosts: [], on_air: null }],
    ["both missing", {}],
    ["failovers null", { hosts: [], on_air: [], failovers_24h: null }],
  ])("HLT-10: tv/cluster %s shows a zero TV line, no crash", async (_, body) => {
    routeGet(get, table({ "/api/tv/cluster": body }));
    open();
    expect(await screen.findByTestId("health-tv")).toHaveTextContent(
      "machines online 0 of 0 · channels on air 0 · failovers (24 h) 0"
    );
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it.each([
    ["processes null", { available: true, processes: null, events: [] }],
    ["events null", { available: true, processes: [], events: null }],
    ["both missing", { available: true }],
  ])("HLT-11: processes doc with %s shows no rows, no crash", async (_, body) => {
    routeGet(get, table({ "/api/system/processes": body }));
    open();
    expect(await within(card("Processes")).findByText("No crashes recorded.")).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
    expect(card("db")).toBeInTheDocument();
  });

  it.each([
    ["null", { caches: null }],
    ["missing", {}],
    ["empty", { caches: [] }],
  ])("HLT-12: caches %s shows No caches registered", async (_, body) => {
    routeGet(get, table({ "/api/admin/caches": body }));
    open();
    expect(await within(card("Caches")).findByText("No caches registered.")).toBeInTheDocument();
  });

  it.each([
    ["PascalCase", { Version: 2, Endpoints: [{}, {}, {}] }, "2", "3"],
    ["camelCase", { version: 5, endpoints: [] }, "5", "0"],
    ["object", { Endpoints: { a: {}, b: {} } }, "0", "2"],
    ["null", { Endpoints: null }, "0", "0"],
    ["empty", {}, "0", "0"],
    ["bad version", { Version: "x" }, "0", "0"],
  ])("HLT-13: budgets %s shape", async (_, body, ver, eps) => {
    routeGet(get, table({ "/api/admin/perf/budgets": body }));
    open();
    const b = await waitFor(() => {
      const c = card("Performance budgets");
      expect(within(c).getByText("Budget version")).toBeInTheDocument();
      return c;
    });
    expect(within(b).getByText("Budget version").nextSibling).toHaveTextContent(ver);
    expect(within(b).getByText("Endpoint budgets").nextSibling).toHaveTextContent(eps);
  });

  it.each([
    [{ status: "ok", services: {} }, "—", "—"],
    [
      {
        status: "ok",
        services: {},
        disk_free_bytes: 0,
        queue_depth: 0,
        transcribe_budget_usd_left: "",
      },
      "0 B",
      "0",
    ],
    [{ status: "ok", services: {}, disk_free_bytes: 1023 }, "1023 B", "—"],
    [{ status: "ok", services: {}, disk_free_bytes: 1099511627776 }, "1.0 TB", "—"],
  ])("HLT-14: optional stats %#", async (body, disk, queue) => {
    routeGet(get, table({ [H]: body }));
    open();
    await loaded();
    expect(within(card("Disk free")).getByText(disk)).toBeInTheDocument();
    expect(within(card("Queue depth")).getByText(queue)).toBeInTheDocument();
    expect(screen.queryByText("Transcribe budget left")).toBeNull();
  });

  it.each([
    ["none", { integration: { configured: false, enabled: false } }, "remote viewing not set up"],
    ["off", { integration: { configured: true, enabled: false } }, "remote viewing off"],
    [
      "problem",
      { integration: { configured: true, enabled: true }, remote: { ready: false } },
      "remote viewing has a problem",
    ],
    ["ready", { integration: { configured: true, enabled: true } }, "remote viewing ready"],
  ])("HLT-16: TV remote line %s", async (_, body, phrase) => {
    routeGet(get, table({ "/api/tv/cluster": { hosts: [], on_air: [] }, "/api/tv/remote": body }));
    open();
    await waitFor(() => expect(screen.getByTestId("health-tv")).toHaveTextContent(` · ${phrase}`));
  });

  it("HLT-16: remote status without integration drops the suffix, no crash", async () => {
    routeGet(get, table({ "/api/tv/remote": {} }));
    open();
    const line = await screen.findByTestId("health-tv");
    await loaded();
    expect(line).not.toHaveTextContent("remote viewing");
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("HLT-17: auto-refresh every 30 s and stops on unmount", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    routeGet(get, table());
    const { unmount } = open();
    await loaded();
    const count = (p: string) =>
      get.mock.calls.filter(([u]) => String(u).split("?")[0] === p).length;
    const pagePaths = [
      H,
      "/api/system/version",
      "/api/admin/caches",
      "/api/admin/perf/budgets",
      "/api/tv/cluster",
      "/api/tv/remote",
    ];
    const before = Object.fromEntries(pagePaths.map((p) => [p, count(p)]));
    const procBefore = count("/api/system/processes");
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    for (const p of pagePaths) expect(count(p)).toBe(before[p] + 1);
    expect(count("/api/system/processes")).toBe(procBefore + 2);
    unmount();
    const total = get.mock.calls.length;
    await act(() => vi.advanceTimersByTimeAsync(60_000));
    expect(get.mock.calls.length).toBe(total);
  });

  it("HLT-18: recovers from an error on the next poll", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let fail = true;
    routeGet(
      get,
      table({
        [H]: () =>
          fail ? Promise.reject(apiErr(500)) : { status: "ok", services: { db: { status: "ok" } } },
      })
    );
    open();
    expect(await screen.findByText(SECTION_ERR)).toBeInTheDocument();
    expect(within(card("Overall status")).getByText("Down")).toBeInTheDocument();
    fail = false;
    await act(() => vi.advanceTimersByTimeAsync(30_000));
    await waitFor(() => expect(screen.queryByText(SECTION_ERR)).toBeNull());
    expect(within(card("Overall status")).getByText("Operational")).toBeInTheDocument();
    expect(card("db")).toBeInTheDocument();
  });

  it("HLT-19: a render crash shows the route error fallback; Back works", async () => {
    const user = userEvent.setup();
    quietCrash();
    routeGet(get, table({ [H]: { status: "ok", services: { db: { status: "ok", reason: {} } } } }));
    open();
    expect(await screen.findByText(CRASH)).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "test shell" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    routeGet(get, table());
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "System health" })
    ).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("HLT-20: a non-JSON (HTML) health body is treated as an error", async () => {
    routeGet(get, table({ [H]: "<html><body>Bad gateway</body></html>" }));
    open();
    expect(await screen.findByText(SECTION_ERR)).toBeInTheDocument();
    expect(within(card("Overall status")).getByText("Down")).toBeInTheDocument();
    expect(screen.queryByText(CRASH)).toBeNull();
  });

  it("HLT-20: truncated JSON (parse error) shows the section error", async () => {
    routeGet(get, table({ [H]: reject(new SyntaxError("Unexpected end of JSON input")) }));
    open();
    expect(await screen.findByText(SECTION_ERR)).toBeInTheDocument();
    expect(within(card("Overall status")).getByText("Down")).toBeInTheDocument();
  });

  it("HLT-22: Arabic, right to left, Arabic-Indic digits", async () => {
    localStorage.setItem("mkt:locale", "ar");
    routeGet(get, table());
    open();
    expect(
      await screen.findByRole("heading", { level: 1, name: "حالة النظام" })
    ).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    expect(document.documentElement.lang).toBe("ar");
    const tv = await screen.findByTestId("health-tv");
    expect(tv).toHaveTextContent("الأجهزة المتصلة ١ من ٢");
    expect(await screen.findByText("١٢")).toBeInTheDocument();
    expect(screen.getByText("thumbs")).toBeInTheDocument();
    expect(screen.getByText("abcdef123456")).toBeInTheDocument();
  });
});
