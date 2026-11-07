// Epic 53 story 53.16: the service admin's problem reports. A home admin's token is refused
// by the API (Access denied); the list filters by app, version, category, status and error
// group; a report shows its merged timeline, and status, issue link and reply are posted.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../api";
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
    renderAt("/admin/reports");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
  });

  it("lists the reports across homes and filters them", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": session(user({ role: "service_admin" })),
      "GET /v1/admin/reports": [200, { reports: [REPORT], total: 1 }],
    });
    renderAt("/admin/reports?app=apple.player");
    const row = await screen.findByTestId("cloud-report-row");
    expect(row).toHaveTextContent("darwish-home");
    expect(row).toHaveTextContent("0.3.95");
    expect(within(row).getByRole("link", { name: "#41" })).toHaveAttribute(
      "href",
      "/admin/reports/41"
    );
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
    renderAt("/admin/reports/41");
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
    renderAt("/admin/reports");
    const nav = await screen.findByRole("navigation", { name: "Admin" });
    expect(within(nav).getByRole("link", { name: "Problem reports" })).toHaveAttribute(
      "href",
      "/admin/reports"
    );
    expect(await screen.findByText("No reports match.")).toBeInTheDocument();
  });
});
