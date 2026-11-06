// Epic 53 story 53.15: the admin's inbox, a report's timeline (the app's and the home's
// lines by time), its status and reply; a viewer sees admins only.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { renderPage, memberUser } from "../../test/pageHarness";
import en from "../../i18n/en.json";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { AdminReport, AdminReports } from "./Reports";

const REPORT = {
  id: 27,
  at: "2026-11-06T10:05:00Z",
  when: "2026-11-06T10:00:00Z",
  name: "sara",
  app: "web",
  version: "0.3.84",
  platform: "web",
  category: "playback",
  status: "new",
  text: "it stopped",
  shared_with_maktaba: false,
  screenshot: false,
  lines: [{ ts: "2026-11-06T10:00:02Z", msg: "app_side" }],
  server: [
    {
      ts: "2026-11-06T10:00:01Z",
      msg: "home_side",
      service: "streaming",
      request_id: "01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60",
    },
  ],
};

describe("Problem reports", () => {
  beforeEach(() => {
    get.mockImplementation((p: string) =>
      p === "/api/reports" ? Promise.resolve({ reports: [REPORT] }) : Promise.resolve(REPORT)
    );
    post.mockResolvedValue({});
  });
  afterEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("lists the reports, and a report merges the app's and the home's lines by time", async () => {
    renderPage(<AdminReports />, { path: "/admin/reports" });
    expect(await screen.findByTestId("report-row")).toHaveTextContent("sara");
  });

  it("a report's timeline, status and reply", async () => {
    renderPage(<AdminReport />, { path: "/admin/reports/27", route: "/admin/reports/:id" });
    const tl = await screen.findByTestId("report-timeline");
    const rows = within(tl).getAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("home_side");
    expect(rows[1]).toHaveTextContent("app_side");
    expect(within(rows[0]).getByRole("link").getAttribute("href")).toContain("request=01932f6e");
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "looking" } });
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/reports/27/status", { status: "looking" })
    );
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Fixed in 0.3.85" } });
    fireEvent.click(screen.getByRole("button", { name: en["report.sendReply"] }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/reports/27/reply", { reply: "Fixed in 0.3.85" })
    );
  });

  it("a viewer sees admins only", async () => {
    renderPage(<AdminReports />, { path: "/admin/reports", user: memberUser });
    expect(await screen.findByText(en["common.forbidden.title"])).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });
});
