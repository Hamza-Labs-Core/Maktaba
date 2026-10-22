// The System health page's status of every part (GET /api/admin/system/status):
// the overall banner, the groups of tiles, history, the help links, the
// fallbacks and both languages.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { renderPage } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    api: { ...actual.api, get, post: vi.fn(), put: vi.fn(), patch: vi.fn(), delete: vi.fn() },
  };
});

import { SystemHealth } from "./SystemHealth";
import { resetStatusStore, type StatusItem, type StatusReport } from "../../lib/systemStatus";

const S = "/api/admin/system/status";
const now = new Date().toISOString();

function item(over: Partial<StatusItem> & Pick<StatusItem, "id" | "kind">): StatusItem {
  return {
    group: "services",
    name: over.id,
    status: "ok",
    code: "",
    reason: "fine",
    last_checked: now,
    help: over.kind,
    ...over,
  };
}

const OK_ITEMS: StatusItem[] = [
  item({
    id: "api",
    kind: "api",
    code: "api_ok",
    critical: true,
    params: { version: "2.1.0", uptime_seconds: 5400 },
  }),
  item({
    id: "postgres",
    kind: "postgres",
    code: "postgres_ok",
    critical: true,
    params: { ms: 3 },
  }),
  item({
    id: "workers",
    kind: "workers",
    group: "workers",
    code: "workers_ok",
    critical: true,
    link: "/queue",
    params: { online: 2, total: 2 },
  }),
  item({
    id: "worker:w1",
    kind: "worker",
    group: "workers",
    name: "NAS",
    code: "worker_ok",
    params: { age_seconds: 4, jobs: 3 },
    details: { role: "main", host: "nas.local", running: { probe: 3 } },
  }),
  item({
    id: "worker:old",
    kind: "worker",
    group: "workers",
    name: "MacBook",
    code: "worker_replaced",
    history: true,
    params: { age_seconds: 7200 },
    details: { role: "transcription", host: "MacBook" },
  }),
  item({
    id: "dependency:resource:disk",
    kind: "dependency",
    group: "dependencies",
    name: "resource:disk",
    code: "dependency_ok",
    link: "/queue/failures",
  }),
  item({
    id: "plex_livetv",
    kind: "plex_livetv",
    group: "integrations",
    code: "plex_ok",
    params: { lineup: 12 },
  }),
  item({
    id: "tv_change",
    kind: "tv_change",
    group: "queues_tv",
    code: "tv_change_ready",
    attention: true,
    link: "/admin/live-tv/changes/c8",
    params: { number: 8, state: "ready" },
  }),
];

function report(over: Partial<StatusReport> = {}): StatusReport {
  return {
    status: "ok",
    code: "overall_ok",
    reason: "Everything is working.",
    checked_at: now,
    items: OK_ITEMS,
    processes_supervised: false,
    ...over,
  };
}

function table(status: unknown): Record<string, unknown> {
  return {
    [S]: status,
    "/api/system/health": { status: "ok", services: {} },
    "/api/system/version": {
      version: "2.1.0",
      build_sha: "abc",
      build_time: "",
      go_version: "go1.23",
      schema_revision: 145,
    },
    "/api/admin/caches": { caches: [] },
    "/api/admin/perf/budgets": {},
    "/api/tv/cluster": { hosts: [], on_air: [] },
    "/api/tv/remote": { integration: { configured: false, enabled: false } },
    "/api/system/processes": { available: false, processes: [], events: [] },
  };
}

const open = () => renderPage(<SystemHealth />, { path: "/admin/health" });
const tile = (id: string) => screen.findByTestId(`health-item-${id}`);

describe("System health: the status of every part", () => {
  beforeEach(() => {
    get.mockReset();
    resetStatusStore();
    localStorage.removeItem("mkt:locale");
  });

  it("all OK: a green banner and every part in its group", async () => {
    routeGet(get, table(report()));
    open();
    const banner = await screen.findByTestId("health-banner");
    expect(banner).toHaveClass("mkt-health-banner--ok");
    expect(banner).toHaveTextContent("All systems OK");
    for (const g of ["services", "workers", "dependencies", "integrations", "queues_tv"]) {
      expect(
        screen.getByRole("heading", { level: 2, name: en[`health.group.${g}` as keyof typeof en] })
      ).toBeInTheDocument();
    }
    // the readyz probes section stays away while it has nothing to show
    expect(screen.queryByRole("heading", { level: 2, name: "Service probes" })).toBeNull();
    const api = await tile("api");
    expect(api).toHaveTextContent("API");
    expect(api).toHaveTextContent("Version 2.1.0, up 1 h 30 min.");
    expect(within(api).getByText("Operational")).toHaveClass("mk-badge--success");
    expect(api).toHaveTextContent(/checked .* ago/);
    expect(within(api).getByRole("link", { name: "Learn more about API" })).toHaveAttribute(
      "href",
      "/help/system-health#item-api"
    );
    const w = await tile("worker:w1");
    expect(w).toHaveTextContent("Seen 4 s ago; running 3 jobs.");
    expect(w).toHaveTextContent(/Main · \u2068?nas\.local\u2069? · running probe 3/);
    expect(within(w).getByRole("link", { name: "Learn more about NAS" })).toHaveAttribute(
      "href",
      "/help/system-health#item-worker"
    );
    expect(within(await tile("workers")).getByRole("link", { name: "Open" })).toHaveAttribute(
      "href",
      "/queue"
    );
    expect(await tile("dependency:resource:disk")).toHaveTextContent("Free disk space");
    const change = await tile("tv_change");
    expect(change).toHaveTextContent("Change #8 is checked and waits for your approval.");
    expect(within(change).getByText("Needs you")).toBeInTheDocument();
    expect(within(change).getByRole("link", { name: "Open" })).toHaveAttribute(
      "href",
      "/admin/live-tv/changes/c8"
    );
  });

  it("keeps a replaced worker under History, never as a failure", async () => {
    routeGet(get, table(report()));
    open();
    const old = await tile("worker:old");
    expect(old.closest("details")).toHaveTextContent("History (1)");
    expect(old).toHaveClass("mkt-health-tile--history");
    expect(within(old).getByText("History")).toBeInTheDocument();
    expect(within(old).queryByText("Down")).toBeNull();
    expect(old).toHaveTextContent("Replaced by a newer worker on the same machine");
    expect(screen.getByTestId("health-banner")).toHaveTextContent("All systems OK");
  });

  it("degraded: a yellow banner naming the parts, each with its reason and help", async () => {
    const items = [
      ...OK_ITEMS,
      item({
        id: "pipeline",
        kind: "pipeline",
        status: "down",
        code: "pipeline_unreachable",
        params: { addr: "127.0.0.1:50051", detail: "connection refused" },
      }),
      item({
        id: "queues",
        kind: "queues",
        group: "queues_tv",
        status: "degraded",
        code: "queues_failing",
        link: "/queue/failures",
        params: { failing: 1, failing_stages: "detect" },
      }),
    ];
    routeGet(get, table(report({ status: "degraded", code: "overall_degraded", items })));
    open();
    const banner = await screen.findByTestId("health-banner");
    expect(banner).toHaveClass("mkt-health-banner--degraded");
    expect(banner).toHaveAttribute("role", "alert");
    expect(banner).toHaveTextContent("Degraded: Pipeline, Queues");
    expect(banner).toHaveTextContent("Does not answer at 127.0.0.1:50051: connection refused");
    expect(within(banner).getByRole("link", { name: "Learn more about Queues" })).toHaveAttribute(
      "href",
      "/help/system-health#item-queues"
    );
    const q = await tile("queues");
    expect(q).toHaveAttribute("data-status", "degraded");
    expect(q).toHaveClass("mkt-health-tile--degraded");
    expect(q).toHaveTextContent("1 failing: detect.");
    expect(within(q).getByRole("link", { name: "Open" })).toHaveAttribute(
      "href",
      "/queue/failures"
    );
    expect(within(await tile("pipeline")).getByText("Down")).toHaveClass("mk-badge--error");
    // the stat card follows the status, not the liveness probe
    const overall = screen.getByText("Overall status", { selector: "strong" }).closest(".mk-card");
    expect(within(overall as HTMLElement).getByText("Degraded")).toBeInTheDocument();
  });

  it("down: a red banner naming only the critical parts", async () => {
    const items = [
      item({
        id: "postgres",
        kind: "postgres",
        status: "down",
        critical: true,
        code: "postgres_unreachable",
        params: { detail: "dial tcp: refused" },
      }),
      item({
        id: "pipeline",
        kind: "pipeline",
        status: "degraded",
        code: "pipeline_not_configured",
      }),
    ];
    routeGet(get, table(report({ status: "down", code: "overall_down", items })));
    open();
    const banner = await screen.findByTestId("health-banner");
    expect(banner).toHaveClass("mkt-health-banner--down");
    expect(banner).toHaveTextContent("Down: Database");
    expect(banner).not.toHaveTextContent("Pipeline");
    expect(banner).toHaveTextContent("The database does not answer: dial tcp: refused");
  });

  it("an unknown reason code shows the server's English reason", async () => {
    const items = [
      item({ id: "x", kind: "brand_new", code: "brand_new_code", reason: "Server says so." }),
    ];
    routeGet(get, table(report({ items })));
    open();
    const x = await tile("x");
    expect(x).toHaveTextContent("Server says so.");
    expect(within(x).getByRole("link", { name: /Learn more/ })).toHaveAttribute(
      "href",
      "/help/system-health#items"
    );
  });

  it("no status (an older server): the page keeps its cards and the probes section", async () => {
    routeGet(
      get,
      table(() => Promise.reject(new Error("404")))
    );
    open();
    expect(await screen.findByRole("heading", { level: 2, name: "Services" })).toBeInTheDocument();
    await waitFor(() => expect(get).toHaveBeenCalledWith(S));
    expect(screen.queryByTestId("health-banner")).toBeNull();
  });

  it("neither the status nor the liveness probe answers: a red banner says so", async () => {
    const t = table(() => Promise.reject(new Error("down")));
    t["/api/system/health"] = () => Promise.reject(new Error("down"));
    routeGet(get, t);
    open();
    const banner = await screen.findByTestId("health-banner");
    expect(banner).toHaveTextContent(en["health.banner.unreachable"]);
  });

  it("Arabic: the banner, names and reasons in Arabic with Arabic-Indic digits", async () => {
    localStorage.setItem("mkt:locale", "ar");
    routeGet(get, table(report()));
    open();
    expect(await screen.findByTestId("health-banner")).toHaveTextContent(ar["health.banner.ok"]);
    const pg = await tile("postgres");
    expect(pg).toHaveTextContent("قاعدة البيانات");
    expect(pg).toHaveTextContent("تجيب في ٣ ملّي ثانية.");
    expect(
      screen.getByRole("heading", { level: 2, name: ar["health.group.queues_tv"] })
    ).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
  });
});

describe("System health catalog", () => {
  // Files outside src/ are read at run time (the web image builds from web/ alone).
  it("every reason code the API sends has its text in English and Arabic", async () => {
    const FS = "node:fs";
    const { readFileSync } = (await import(/* @vite-ignore */ FS)) as {
      readFileSync: (path: string, encoding: "utf8") => string;
    };
    const src = readFileSync("../api/internal/handlers/systemstatus/checks.go", "utf8");
    const codes = new Set(
      [...src.matchAll(/\.set\(\s*Status\w+,\s*"([a-z_]+)"/g)].map((m) => m[1]!)
    );
    for (const m of src.matchAll(/return Status\w+, "([a-z_]+)"/g)) codes.add(m[1]!);
    codes.add("check_timeout");
    expect(codes.size).toBeGreaterThan(50);
    const enC = en as Record<string, string>;
    const arC = ar as Record<string, string>;
    for (const c of codes) {
      expect(enC[`health.reason.${c}`], `en health.reason.${c}`).toBeTruthy();
      expect(arC[`health.reason.${c}`], `ar health.reason.${c}`).toBeTruthy();
    }
  });
});
