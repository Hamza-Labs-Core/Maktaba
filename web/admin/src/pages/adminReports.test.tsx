// Epic 53 story 53.16: the service admin's problem reports. A home admin's token is refused
// by the API (Access denied); the list filters by app, version, category, status and error
// group; a report shows its merged timeline, and status, issue link and reply are posted.
// Its Apple crashes read as functions and lines where the release's symbols are uploaded
// (53.14), and the Crash symbols page lists the uploaded tables.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "@cloud-shared/api";
import { renderAt, session, stubFetch, user } from "../test/harness";

beforeEach(() => setAccessToken(null));
afterEach(() => vi.unstubAllGlobals());

const REPORT = {
  id: 41,
  server_id: "s1",
  home: "darwish-home",
  home_report_id: 7,
  received_at: "2026-11-07T12:00:00Z",
  happened_at: "2026-11-07T11:50:00Z",
  app: "apple.player",
  version: "0.3.95",
  platform: "ios",
  category: "playback",
  status: "new",
  screenshot: false,
  fingerprints: ["fp-1"],
};

describe("admin problem reports", () => {
  it("renders Access denied for an account without the service admin role", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "user" })),
      "GET /v1/admin/reports": [403, { error: "admin_access_denied", message: "denied" }],
    });
    renderAt("/reports");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
  });

  it("lists the reports across homes and filters them", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports": [200, { reports: [REPORT], total: 1 }],
    });
    renderAt("/reports?app=apple.player");
    const row = await screen.findByTestId("cloud-report-row");
    expect(row).toHaveTextContent("darwish-home");
    expect(row).toHaveTextContent("0.3.95");
    expect(within(row).getByRole("link", { name: "#41" })).toHaveAttribute("href", "/reports/41");
    expect(f.find("GET", "/v1/admin/reports")[0].path).toContain("app=apple.player");
    const u = userEvent.setup();
    await u.selectOptions(screen.getByLabelText("Status"), "fixed");
    await waitFor(() =>
      expect(f.find("GET", "/v1/admin/reports").at(-1)!.path).toContain("status=fixed")
    );
  });

  it("a report's timeline, and its status, issue and reply", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [
        200,
        {
          ...REPORT,
          owner_email: "owner@example.org",
          body: {
            text: "the video stopped",
            lines: [{ ts: "2026-11-07T11:50:02Z", msg: "app_side" }],
            server: [{ ts: "2026-11-07T11:50:01Z", msg: "home_side", service: "streaming" }],
          },
        },
      ],
      "POST /v1/admin/reports/41/status": [204, null],
      "POST /v1/admin/reports/41/issue": [204, null],
      "POST /v1/admin/reports/41/reply": [204, null],
    });
    renderAt("/reports/41");
    const tl = await screen.findByTestId("cloud-report-timeline");
    const rows = within(tl).getAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("home_side");
    expect(rows[1]).toHaveTextContent("app_side");
    expect(screen.getByText("the video stopped")).toBeInTheDocument();
    const u = userEvent.setup();
    await u.selectOptions(screen.getByLabelText("Status"), "fixed");
    await u.type(screen.getByLabelText("Fixed in version"), "0.3.96");
    await u.click(screen.getByRole("button", { name: "Save the status" }));
    await waitFor(() =>
      expect(f.find("POST", "/v1/admin/reports/41/status")[0]?.body).toEqual({
        status: "fixed",
        fixed_in: "0.3.96",
      })
    );
    await u.type(screen.getByLabelText("GitHub issue"), "https://github.com/x/y/issues/1");
    await u.click(screen.getByRole("button", { name: "Save the link" }));
    await waitFor(() =>
      expect(f.find("POST", "/v1/admin/reports/41/issue")[0]?.body).toEqual({
        url: "https://github.com/x/y/issues/1",
      })
    );
    await u.type(screen.getByLabelText("Reply to the home"), "Fixed in 0.3.96");
    await u.click(screen.getByRole("button", { name: "Send the reply" }));
    await waitFor(() =>
      expect(f.find("POST", "/v1/admin/reports/41/reply")[0]?.body).toEqual({
        reply: "Fixed in 0.3.96",
      })
    );
  });

  it("links the reports from the admin nav", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports": [200, { reports: [], total: 0 }],
    });
    renderAt("/reports");
    const nav = await screen.findByRole("navigation", { name: "Admin" });
    expect(within(nav).getByRole("link", { name: "Problem reports" })).toHaveAttribute(
      "href",
      "/reports"
    );
    expect(await screen.findByText("No reports match.")).toBeInTheDocument();
  });

  it("a report's crash, symbolicated where the release's symbols are uploaded", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [
        200,
        {
          ...REPORT,
          body: {},
          crashes: [
            {
              type: "EXC_BAD_ACCESS",
              value: "SIGSEGV",
              frames: [
                {
                  raw: "Maktaba + 0x1a2b",
                  in_app: true,
                  symbol: {
                    function: "Maktaba.Player.play",
                    file: "/src/apps/apple/Player.swift",
                    line: 42,
                  },
                },
                { raw: "libsystem_c.dylib + 0x10", in_app: false },
              ],
              missing: [{ code_file: "libsystem_c.dylib", debug_id: "1111" }],
            },
          ],
        },
      ],
    });
    renderAt("/reports/41");
    expect(await screen.findByRole("heading", { name: "Crashes" })).toBeInTheDocument();
    expect(screen.getByText("EXC_BAD_ACCESS · SIGSEGV")).toBeInTheDocument();
    const frames = within(screen.getByTestId("crash-frames")).getAllByRole("listitem");
    expect(frames[0]).toHaveTextContent("Maktaba.Player.play (Player.swift:42) · Maktaba + 0x1a2b");
    expect(frames[1]).toHaveTextContent(/^libsystem_c\.dylib \+ 0x10$/);
    expect(screen.getByTestId("crash-missing")).toHaveTextContent(
      "No symbols for libsystem_c.dylib (1111)."
    );
  });

  it("a report without crashes shows no crash section", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports/41": [200, { ...REPORT, body: {} }],
    });
    renderAt("/reports/41");
    expect(await screen.findByTestId("cloud-report")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Crashes" })).not.toBeInTheDocument();
  });

  it("lists the uploaded crash symbols, linked from the nav", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/symbols": [
        200,
        {
          tables: [
            {
              debug_id: "9e40f33c5e1e37528634a1cd1b5ae44f",
              code_file: "Maktaba",
              arch: "arm64",
              version: "0.3.98",
              uploaded_at: "2026-11-07T12:00:00Z",
              size: 2_500_000,
            },
          ],
        },
      ],
    });
    renderAt("/symbols");
    const table = await screen.findByTestId("symbol-tables");
    expect(table).toHaveTextContent("9e40f33c5e1e37528634a1cd1b5ae44f");
    expect(table).toHaveTextContent("0.3.98");
    expect(table).toHaveTextContent("2,442 KB");
    const nav = screen.getByRole("navigation", { name: "Admin" });
    expect(within(nav).getByRole("link", { name: "Crash symbols" })).toHaveAttribute(
      "href",
      "/symbols"
    );
  });

  it("says when no symbols are uploaded", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/symbols": [200, { tables: [] }],
    });
    renderAt("/symbols");
    expect(await screen.findByText("No symbols uploaded yet.")).toBeInTheDocument();
  });

  it("renders Access denied on the symbols page for a normal account", async () => {
    stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "user" })),
      "GET /v1/admin/symbols": [403, { error: "admin_access_denied", message: "denied" }],
    });
    renderAt("/symbols");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
  });
});
