import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../api";
import { ME, renderAt, session, stubFetch, user } from "../test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => vi.unstubAllGlobals());

const DENIED: [number, unknown] = [403, { error: "admin_access_denied", message: "denied" }];

describe("admin console", () => {
  it("hides the Admin nav link from a normal account", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "user" })),
      "GET /v1/account/me": [200, ME],
      "GET /v1/account/sessions": [200, { sessions: [] }],
    });
    renderAt("/account");
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(nav).getByRole("link", { name: "Account" })).toBeInTheDocument();
    expect(within(nav).getByRole("link", { name: "Billing" })).toBeInTheDocument();
    expect(within(nav).queryByRole("link", { name: "Admin" })).not.toBeInTheDocument();
  });

  it("shows the Admin nav link to a service admin", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/account/me": [200, { ...ME, role: "service_admin" }],
      "GET /v1/account/sessions": [200, { sessions: [] }],
    });
    renderAt("/account");
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(within(nav).getByRole("link", { name: "Admin" })).toHaveAttribute("href", "/admin");
  });

  it("renders Access denied when the API answers 403", async () => {
    // the URL is reachable by anyone; the API is the authority
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "user" })),
      "GET /v1/admin/accounts": DENIED,
    });
    renderAt("/admin/accounts");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("renders Access denied on the overview too", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/fleet": DENIED,
      "GET /v1/admin/revenue": DENIED,
    });
    renderAt("/admin");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
  });

  it("sends search, filters and paging to the accounts list", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/accounts": [
        200,
        {
          accounts: [
            {
              id: "u2",
              email: "bad@example.com",
              plan: "pro",
              status: "active",
              role: "user",
              created_at: "2026-01-01T00:00:00Z",
              servers: 2,
            },
          ],
          total: 60,
        },
      ],
    });
    renderAt("/admin/accounts?status=active&page=2");
    expect(await screen.findByRole("link", { name: "bad@example.com" })).toHaveAttribute(
      "href",
      "/admin/accounts/u2"
    );
    const first = f.find("GET", "/v1/admin/accounts")[0].path;
    expect(first).toContain("status=active");
    expect(first).toContain("limit=25");
    expect(first).toContain("offset=25");
    const u = userEvent.setup();
    await u.type(screen.getByLabelText("Search email or name"), "bad");
    await u.click(screen.getByRole("button", { name: "Search" }));
    await waitFor(() =>
      expect(f.find("GET", "/v1/admin/accounts").at(-1)!.path).toContain("q=bad")
    );
    // a new search starts at the first page
    expect(f.find("GET", "/v1/admin/accounts").at(-1)!.path).toContain("offset=0");
  });

  it("requires a reason to suspend, and posts it", async () => {
    let status = "active";
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/accounts/u2": () =>
        new Response(
          JSON.stringify({
            account: {
              id: "u2",
              email: "bad@example.com",
              plan: "pro",
              status,
              role: "user",
              created_at: "2026-01-01T00:00:00Z",
              servers: 0,
            },
            subscription: { plan: "pro", status: "active" },
            servers: [],
            usage: { month: "2026-10", used_bytes: 5e9, limit_bytes: 1e11 },
            sessions_active: 1,
            abuse: [],
            audit: [],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        ),
      "POST /v1/admin/accounts/u2/suspend": () => {
        status = "suspended";
        return new Response(null, { status: 204 });
      },
    });
    renderAt("/admin/accounts/u2");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Suspend" }));
    const dialog = await screen.findByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "Suspend" });
    expect(confirm).toBeDisabled();
    await u.type(within(dialog).getByLabelText(/Reason/), "   ");
    expect(confirm).toBeDisabled();
    await u.type(within(dialog).getByLabelText(/Reason/), "chargeback fraud");
    expect(confirm).toBeEnabled();
    await u.click(confirm);
    await waitFor(() => expect(f.find("POST", "/v1/admin/accounts/u2/suspend")).toHaveLength(1));
    expect(f.find("POST", "/v1/admin/accounts/u2/suspend")[0].body).toEqual({
      reason: "chargeback fraud",
    });
    // reloaded: now offers Unsuspend
    expect(await screen.findByRole("button", { name: "Unsuspend" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps the dialog open with the error when suspending is refused", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/accounts/u3": [
        200,
        {
          account: {
            id: "u3",
            email: "ops@example.com",
            plan: "free",
            status: "active",
            role: "service_admin",
            created_at: "2026-01-01T00:00:00Z",
            servers: 0,
          },
          subscription: null,
          servers: [],
          usage: { month: "2026-10", used_bytes: 0, limit_bytes: 0 },
          sessions_active: 0,
          abuse: [],
          audit: [],
        },
      ],
      "POST /v1/admin/accounts/u3/suspend": [409, { error: "cannot_suspend_admin", message: "no" }],
    });
    renderAt("/admin/accounts/u3");
    const u = userEvent.setup();
    await u.click(await screen.findByRole("button", { name: "Suspend" }));
    const dialog = await screen.findByRole("dialog");
    await u.type(within(dialog).getByLabelText(/Reason/), "test");
    await u.click(within(dialog).getByRole("button", { name: "Suspend" }));
    expect(await within(dialog).findByText(/can't be suspended/)).toBeInTheDocument();
  });

  it("renders Access denied on the usage page", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "user" })),
      "GET /v1/admin/usage": DENIED,
    });
    renderAt("/admin/usage");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("shows the usage tables and changes the period", async () => {
    const usage = {
      days: 30,
      since: "2026-09-27T00:00:00Z",
      installs: { homes: 12, app_installs: 40, by_app: [{ app: "web", installs: 30 }] },
      server_versions: [{ version: "0.3.26", homes: 9 }],
      app_versions: [{ app: "web", version: "1.2.3", installs: 30 }],
      server_platforms: [{ os: "darwin", arch: "arm64", homes: 7 }],
      install_kinds: [{ install_kind: "mac-app", homes: 7 }],
      app_platforms: [{ app: "tv", platform: "tvos", form_factor: "tv", installs: 10 }],
      apps_in_use: [{ app: "web", platform: "web", homes: 11, installs: 30 }],
      features: [{ feature: "live_tv", homes: 6, share: 0.5 }],
      feature_use: [{ app: "web", feature: "library", total: 321, installs: 20 }],
      errors: [
        {
          fingerprint: "f1",
          source: "server",
          app: "server",
          message: "db timeout",
          count: 77,
          installs: 4,
          versions: ["0.3.26", "0.3.25"],
          first_seen: "2026-10-01T00:00:00Z",
          last_seen: "2026-10-25T00:00:00Z",
        },
      ],
      vitals: [{ metric: "LCP", p75: 1834.4, samples: 90 }],
    };
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/usage": [200, usage],
    });
    renderAt("/admin/usage");
    expect(await screen.findByText("db timeout")).toBeInTheDocument();
    expect(screen.getByText("Homes", { selector: ".mkc-muted" })).toBeInTheDocument();
    expect(screen.getByText("0.3.26, 0.3.25")).toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
    expect(screen.getByText("1,834")).toBeInTheDocument();
    expect(screen.getByText("321")).toBeInTheDocument();
    expect(f.find("GET", "/v1/admin/usage")[0].path).toContain("days=30");
    const u = userEvent.setup();
    await u.selectOptions(screen.getByLabelText("Period"), "90");
    await waitFor(() => expect(f.find("GET", "/v1/admin/usage").at(-1)!.path).toContain("days=90"));
  });

  it("links the usage page from the admin nav", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/usage": DENIED,
    });
    renderAt("/admin/usage");
    const nav = await screen.findByRole("navigation", { name: "Admin" });
    expect(within(nav).getByRole("link", { name: "Usage" })).toHaveAttribute(
      "href",
      "/admin/usage"
    );
  });
});
