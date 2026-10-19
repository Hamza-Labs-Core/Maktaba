// Cloud server status page cases from web/test-cases/cloud-status.md.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});

import { ApiError } from "../../lib/api";
import type { User } from "../../lib/auth";
import { renderPage, quietCrash, memberUser, adminUser } from "../../test/pageHarness";
import ar from "../../i18n/ar.json";
import { ServerStatus } from "./ServerStatus";

const VERSION = { version: "1.9.0", build_sha: "abcdef0123456789" };
const NOT_CONFIGURED = {
  status: "ok",
  services: { api: { status: "ok" }, streaming: { status: "ok" } },
};
const relay = (svc: unknown) => ({
  status: "ok",
  services: { api: { status: "ok" }, "cloud-relay": svc },
});
const CONNECTED = relay({
  status: "ok",
  checks: { heartbeat: { status: "ok", reason: "2s ago" } },
});

function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", status, title, detail });
}

type Reply = unknown | Error | (() => Promise<unknown>);

/** Health replies in order (last one repeats); version is fixed. */
function serve(health: Reply[], version: Reply = VERSION) {
  let i = 0;
  const answer = (r: Reply) =>
    typeof r === "function"
      ? (r as () => Promise<unknown>)()
      : r instanceof Error
        ? Promise.reject(r)
        : Promise.resolve(r);
  get.mockImplementation((url: string) => {
    if (url === "/api/system/health") return answer(health[Math.min(i++, health.length - 1)]);
    if (url === "/api/system/version") return answer(version);
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const calls = (path: string) => get.mock.calls.filter(([u]) => u === path).length;

function open(opts: { user?: User | null } = {}) {
  return renderPage(<ServerStatus />, { path: "/cloud/status", ...opts });
}

const card = (label: string) => screen.getByText(label).closest(".mkt-stat") as HTMLElement;

async function tick(ms = 30_000) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

describe("ServerStatus (test-cases/cloud-status.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
    serve([CONNECTED]);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it.each([
    ["other services only", NOT_CONFIGURED],
    ["services {}", { status: "ok", services: {} }],
  ])("CST-01: no cloud service (%s) shows not configured and setup steps", async (_, h) => {
    serve([h]);
    open();
    expect(await screen.findByText("Cloud relay not configured")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 1, name: "Cloud server status" })
    ).toBeInTheDocument();
    expect(screen.getByText("Auto-refreshes every 30s")).toBeInTheDocument();
    expect(
      screen.getByText("This server isn't connected to a Maktaba Cloud relay yet.")
    ).toBeInTheDocument();
    expect(screen.getByText("Connect to Maktaba Cloud")).toBeInTheDocument();
    expect(screen.getAllByRole("listitem")).toHaveLength(3);
    expect(screen.queryByText("Connected")).toBeNull();
  });

  it("CST-02: cloud relay connected", async () => {
    open();
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(card("Tunnel")).toHaveTextContent("Connected");
    await waitFor(() => expect(card("Server ID")).toHaveTextContent("abcdef012345"));
    expect(screen.getByText("abcdef012345")).toHaveClass("mkt-mono");
    expect(card("Last heartbeat")).toHaveTextContent("2s ago");
    expect(card("Bandwidth")).toHaveTextContent("Not reported");
    expect(screen.queryByText("Cloud relay not configured")).toBeNull();
    expect(screen.queryByText("Connect to Maktaba Cloud")).toBeNull();
  });

  it.each([
    ["down", { status: "down", reason: "dial tcp: i/o timeout" }, "dial tcp: i/o timeout"],
    ["degraded", { status: "degraded" }, "Not reported"],
    ["unknown", { status: "unknown" }, "Not reported"],
  ])("CST-03: relay %s shows Disconnected", async (_, svc, heartbeat) => {
    serve([relay(svc)]);
    open();
    expect(await screen.findByText("Disconnected")).toBeInTheDocument();
    expect(card("Last heartbeat")).toHaveTextContent(heartbeat);
    await waitFor(() => expect(card("Server ID")).toHaveTextContent("abcdef012345"));
  });

  it.each(["ok", "OK", "up", "Up", "healthy", "READY"])(
    "CST-04: status %s counts as Connected",
    async (status) => {
      serve([relay({ status })]);
      open();
      expect(await screen.findByText("Connected")).toBeInTheDocument();
    }
  );

  it("CST-04: any other status (starting) is Disconnected", async () => {
    serve([relay({ status: "starting" })]);
    open();
    expect(await screen.findByText("Disconnected")).toBeInTheDocument();
  });

  it.each([
    [
      "checks win over reason",
      { status: "ok", checks: { heartbeat: { status: "ok", reason: "5s ago" } }, reason: "x" },
      "5s ago",
    ],
    ["reason without checks", { status: "ok", reason: "last seen 10s ago" }, "last seen 10s ago"],
    [
      "heartbeat without reason",
      { status: "ok", checks: { heartbeat: { status: "ok" } } },
      "Not reported",
    ],
    ["empty reason", { status: "ok", reason: "" }, "Not reported"],
  ])("CST-05: heartbeat text (%s)", async (_, svc, text) => {
    serve([relay(svc)]);
    open();
    await screen.findByText("Connected");
    expect(card("Last heartbeat")).toHaveTextContent(text);
  });

  it.each([
    ["500", problem(500, "Internal Server Error")],
    ["network failure", new TypeError("Failed to fetch")],
    ["empty build_sha", { version: "1.9.0", build_sha: "" }],
    ["no build_sha", { version: "1.9.0" }],
    ["null build_sha", { build_sha: null }],
  ])("CST-06: version %s falls back to the service name", async (_, version) => {
    serve([CONNECTED], version);
    open();
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    await waitFor(() => expect(calls("/api/system/version")).toBe(1));
    expect(card("Server ID")).toHaveTextContent("cloud-relay");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["500", problem(500, "Internal Server Error")],
    ["network failure", new TypeError("Failed to fetch")],
    ["502 HTML", problem(502, "Bad Gateway", "<html>bad gateway</html>")],
  ])("CST-07: health %s shows the Down card", async (_, err) => {
    serve([err]);
    open();
    expect(await screen.findByText("Down")).toBeInTheDocument();
    expect(card("Tunnel")).toHaveTextContent("Could not reach the server health endpoint.");
    expect(screen.queryByText("Connect to Maktaba Cloud")).toBeNull();
    expect(screen.queryByText("Connected")).toBeNull();
    // No Retry button: the next poll retries.
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  });

  // Desired: 401 -> /login, 403 -> a permission message, not "server down".
  // Needs shared 401 handling (src/lib/api.ts / auth.tsx) and a new i18n key.
  it.fails("CST-08: health 401 sends the user to sign in instead of 'Down'", async () => {
    serve([problem(401, "Unauthorized")]);
    open();
    expect(await screen.findByText("login page (from /cloud/status)")).toBeInTheDocument();
  });

  it.fails("CST-08: health 403 is not reported as the server being down", async () => {
    serve([problem(403, "Forbidden")]);
    open();
    await waitFor(() => expect(calls("/api/system/health")).toBe(1));
    await act(async () => {});
    expect(screen.queryByText("Down")).toBeNull();
  });

  it("CST-09: polls every 30 s and recovers after an error", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve([new TypeError("Failed to fetch"), CONNECTED]);
    open();
    expect(await screen.findByText("Down")).toBeInTheDocument();
    expect(calls("/api/system/health")).toBe(1);
    expect(calls("/api/system/version")).toBe(1);
    await tick(29_000);
    expect(calls("/api/system/health")).toBe(1);
    await tick(1_000);
    expect(calls("/api/system/health")).toBe(2);
    expect(calls("/api/system/version")).toBe(2);
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(screen.queryByText("Down")).toBeNull();
    expect(screen.queryByText("Loading…")).toBeNull();
  });

  it("CST-10: the link going down while the page is open is shown each poll", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    serve([CONNECTED, relay({ status: "down" }), new TypeError("Failed to fetch"), NOT_CONFIGURED]);
    open();
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    await tick();
    expect(await screen.findByText("Disconnected")).toBeInTheDocument();
    await tick();
    expect(await screen.findByText("Down")).toBeInTheDocument();
    expect(screen.queryByText("Connected")).toBeNull();
    await tick();
    expect(await screen.findByText("Cloud relay not configured")).toBeInTheDocument();
    expect(screen.getByText("Connect to Maktaba Cloud")).toBeInTheDocument();
  });

  it("CST-11: polling stops when leaving the page", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const errors = vi.spyOn(console, "error");
    open();
    await screen.findByText("Connected");
    await userEvent
      .setup({ advanceTimers: vi.advanceTimersByTime })
      .click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    await tick(90_000);
    expect(calls("/api/system/health")).toBe(1);
    expect(calls("/api/system/version")).toBe(1);
    expect(errors).not.toHaveBeenCalled();
    errors.mockRestore();
  });

  it.each([
    ["services null", { status: "ok", services: null }],
    ["services missing", { status: "ok" }],
  ])("CST-12: %s shows the not-configured state, no crash", async (_, h) => {
    serve([h]);
    open();
    expect(await screen.findByText("Cloud relay not configured")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["status null", { status: null }, "Disconnected"],
    ["empty object", {}, "Disconnected"],
    ["service null", null, "Disconnected"],
    ["checks null", { status: "ok", checks: null }, "Connected"],
    ["heartbeat null", { status: "ok", checks: { heartbeat: null } }, "Connected"],
  ])("CST-13: cloud service with %s does not crash", async (_, svc, badge) => {
    serve([relay(svc)]);
    open();
    expect(await screen.findByText(badge)).toBeInTheDocument();
    expect(card("Last heartbeat")).toHaveTextContent("Not reported");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["malformed JSON", new SyntaxError("Unexpected token")],
    ["HTML body", "<!doctype html><html></html>"],
    ["null body", null],
  ])("CST-14: %s shows the Down card", async (_, reply) => {
    serve([reply]);
    open();
    expect(await screen.findByText("Down")).toBeInTheDocument();
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["Cloud", { Cloud: { status: "ok" } }, "Connected", "Cloud"],
    ["relay", { relay: { status: "ok" } }, "Connected", "relay"],
    ["tunnel", { tunnel: { status: "down" } }, "Disconnected", "tunnel"],
  ])("CST-15: service named %s is matched case-insensitively", async (_, services, badge, name) => {
    serve([{ status: "ok", services }], problem(500, "x"));
    open();
    expect(await screen.findByText(badge)).toBeInTheDocument();
    await waitFor(() => expect(card("Server ID")).toHaveTextContent(name));
  });

  // The first key that merely contains "cloud" wins, so an unrelated
  // service can be shown as the relay (false green). Fix: match the relay's
  // exact service name in findCloudService (ServerStatus.tsx) once the
  // server's name for it is fixed.
  it.fails("CST-15: cloud_storage is not mistaken for the relay", async () => {
    serve([
      { status: "ok", services: { cloud_storage: { status: "ok" }, relay: { status: "down" } } },
    ]);
    open();
    expect(await screen.findByText("Disconnected")).toBeInTheDocument();
  });

  it.fails("CST-15: cloudflare_stream is not mistaken for the relay", async () => {
    serve([{ status: "ok", services: { cloudflare_stream: { status: "ok" } } }]);
    open();
    expect(await screen.findByText("Cloud relay not configured")).toBeInTheDocument();
  });

  it.each([
    ["member", memberUser],
    ["admin", adminUser],
  ])("CST-17: %s sees the same page (no admin gate)", async (_, user) => {
    open({ user });
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(screen.queryByText("Admins only")).toBeNull();
  });

  it("CST-17: signed out redirects to /login with no polling", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open({ user: null });
    expect(await screen.findByText("login page (from /cloud/status)")).toBeInTheDocument();
    await tick(60_000);
    expect(get).not.toHaveBeenCalled();
  });

  it("CST-18: crash during render shows the route fallback; Back recovers; no polling after", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const quiet = quietCrash();
    serve([relay({ status: "ok", reason: { x: 1 } })]);
    open();
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "This page hit an error and could not be shown."
    );
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(await screen.findByText("elsewhere")).toBeInTheDocument();
    const before = calls("/api/system/health");
    await tick(60_000);
    expect(calls("/api/system/health")).toBe(before);

    serve([CONNECTED]);
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    quiet.mockRestore();
  });

  it("CST-19: coming back mounts one fresh poll (not two)", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    open();
    await screen.findByText("Connected");
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    await user.click(await screen.findByRole("button", { name: "go back" }));
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(calls("/api/system/health")).toBe(2);
    await tick();
    expect(calls("/api/system/health")).toBe(3);
    expect(calls("/api/system/version")).toBe(3);
  });

  it.each([
    ["not configured", NOT_CONFIGURED, "cloud.status.notConfigured.title"],
    ["connected", CONNECTED, "cloud.status.connected"],
    ["down", new TypeError("Failed to fetch"), "health.status.down"],
  ] as const)("CST-20: Arabic / RTL (%s)", async (_, h, key) => {
    localStorage.setItem("mkt:locale", "ar");
    serve([h]);
    open();
    expect(await screen.findByText(ar[key])).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(
      screen.getByRole("heading", { level: 1, name: ar["cloud.status.title"] })
    ).toBeInTheDocument();
    expect(screen.getByText(ar["health.autoRefresh"])).toBeInTheDocument();
  });
});
