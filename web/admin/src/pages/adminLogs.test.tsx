// Epic 53 story 53.17: the service admin's Logs and Errors. A home admin's token is refused
// by the API (Access denied); a search asks the cloud's index with its filters; Errors shows
// the cloud's own groups beside the homes' anonymous ones, each with its reports, and says so
// when the witness's index is not set up yet.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setStaffToken } from "../api";
import { renderAt, staffSession, stubFetch } from "../test/harness";

beforeEach(() => setStaffToken(null));
afterEach(() => vi.unstubAllGlobals());

const admin = () => staffSession();

describe("admin logs and errors", () => {
  it("renders Access denied for an account without the role", async () => {
    stubFetch({
      "POST /v1/staff/refresh": staffSession([]),
      "GET /v1/admin/logs/search": [403, { error: "admin_access_denied", message: "denied" }],
    });
    renderAt("/logs");
    expect(await screen.findByText("Access denied")).toBeInTheDocument();
  });

  it("searches the cloud's lines with the filters, and follows a request", async () => {
    const f = stubFetch({
      "POST /v1/staff/refresh": admin(),
      "GET /v1/admin/logs/search": [
        200,
        {
          lines: [
            {
              ts: "2026-11-07T12:00:00Z",
              level: "warn",
              service: "cloud.relay",
              node: "A",
              msg: "relay_peer_unreachable",
              request_id: "0192f0c4-1111-7000-8000-000000000001",
              home_h: "0123456789abcdef",
            },
          ],
        },
      ],
    });
    renderAt("/logs?node=A");
    const lines = await screen.findByTestId("cloud-log-lines");
    expect(lines).toHaveTextContent("relay_peer_unreachable");
    expect(lines).toHaveTextContent("cloud.relay A");
    expect(f.find("GET", "/v1/admin/logs/search")[0].path).toContain("node=A");
    const u = userEvent.setup();
    await u.type(screen.getByLabelText("Words"), "peer");
    await u.click(screen.getByRole("button", { name: "Search" }));
    await waitFor(() =>
      expect(f.find("GET", "/v1/admin/logs/search").at(-1)!.path).toContain("q=peer")
    );
    await u.click(within(lines).getByRole("link", { name: "0192f0c4" }));
    await waitFor(() =>
      expect(f.find("GET", "/v1/admin/logs/search").at(-1)!.path).toContain(
        "request_id=0192f0c4-1111-7000-8000-000000000001"
      )
    );
    expect(await screen.findByText(/Following request/)).toBeInTheDocument();
  });

  it("shows the cloud's errors beside the homes', with their reports", async () => {
    stubFetch({
      "POST /v1/staff/refresh": admin(),
      "GET /v1/admin/errors": [
        200,
        {
          days: 14,
          cloud: [
            {
              fp: "c1",
              service: "cloud.api",
              code: "db_timeout",
              title: "The database did not answer",
              last_seen: "2026-11-07T12:00:00Z",
              count: 100,
              count_24h: 40,
              status: "new",
              reports: 0,
            },
          ],
          homes: [
            {
              fp: "h1",
              source: "server",
              app: "server",
              message: "segment_missing",
              count: 42,
              installs: 7,
              last_version: "0.3.95",
              last_seen: "2026-11-07T11:00:00Z",
              reports: 3,
            },
          ],
        },
      ],
    });
    renderAt("/errors");
    const cloud = await screen.findByTestId("cloud-error-groups");
    expect(cloud).toHaveTextContent("The database did not answer");
    expect(cloud).toHaveTextContent("100");
    const homes = screen.getByTestId("home-error-groups");
    expect(homes).toHaveTextContent("segment_missing");
    expect(within(homes).getByRole("link", { name: "3 reports" })).toHaveAttribute(
      "href",
      "/reports?fp=h1"
    );
  });

  it("says when the witness's index is not set up, and still shows the homes' errors", async () => {
    stubFetch({
      "POST /v1/staff/refresh": admin(),
      "GET /v1/admin/errors": [200, { days: 14, cloud_error: "logs_unavailable", homes: [] }],
    });
    renderAt("/errors");
    expect(await screen.findByTestId("cloud-errors-unavailable")).toHaveTextContent(
      "is not set up yet"
    );
  });

  it("links Logs and Errors from the admin nav", async () => {
    stubFetch({
      "POST /v1/staff/refresh": admin(),
      "GET /v1/admin/errors": [200, { days: 14, cloud: [], homes: [] }],
    });
    renderAt("/errors");
    const nav = await screen.findByRole("navigation", { name: "Admin" });
    expect(within(nav).getByRole("link", { name: "Errors" })).toHaveAttribute("href", "/errors");
    expect(within(nav).getByRole("link", { name: "Logs" })).toHaveAttribute("href", "/logs");
  });
});
