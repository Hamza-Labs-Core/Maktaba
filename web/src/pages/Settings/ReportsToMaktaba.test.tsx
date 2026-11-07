// Epic 53 story 53.16: Settings → Privacy → Problem reports to Maktaba, an admin's: the
// switch (needs the cloud link), the counts, and "Delete what Maktaba has" saying Deleted.
import { describe, it, expect, vi, afterEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderPage } from "../../test/pageHarness";
import en from "../../i18n/en.json";

const { get, put, del } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), del: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put, delete: del } };
});

import { ReportsToMaktaba } from "./ReportsToMaktaba";

describe("Problem reports to Maktaba", () => {
  afterEach(() => {
    get.mockReset();
    put.mockReset();
    del.mockReset();
  });

  it("turns sending on, off by default", async () => {
    get.mockResolvedValueOnce({ available: true, enabled: false, delete: { state: "none" } });
    get.mockResolvedValue({
      available: true,
      enabled: true,
      delete: { state: "none" },
      sent: 2,
      queued: 1,
    });
    put.mockResolvedValue({ enabled: true });
    renderPage(<ReportsToMaktaba />, { path: "/settings/privacy" });
    const sw = await screen.findByRole("switch", { name: en["privacy.reportsToMaktaba"] });
    await waitFor(() => expect(sw).toBeEnabled());
    expect(sw).not.toBeChecked();
    expect(screen.getByText(en["privacy.reportsToMaktaba.help"])).toBeInTheDocument();
    fireEvent.click(sw);
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/reports/maktaba", { enabled: true })
    );
    expect(await screen.findByTestId("reports-to-maktaba-counts")).toHaveTextContent("2");
  });

  it("needs the cloud link", async () => {
    get.mockResolvedValue({ available: false, enabled: false });
    renderPage(<ReportsToMaktaba />, { path: "/settings/privacy" });
    expect(await screen.findByText(en["privacy.reportsToMaktaba.notLinked"])).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: en["privacy.reportsToMaktaba"] })).toBeDisabled();
    expect(screen.queryByRole("button", { name: en["privacy.deleteAtMaktaba"] })).toBeNull();
  });

  it("deletes what Maktaba has and says Deleted", async () => {
    get.mockResolvedValueOnce({ available: true, enabled: true, delete: { state: "none" } });
    get.mockResolvedValue({
      available: true,
      enabled: true,
      delete: { state: "done", deleted_at: "2026-11-07T12:00:00Z" },
    });
    del.mockResolvedValue({ state: "done" });
    renderPage(<ReportsToMaktaba />, { path: "/settings/privacy" });
    fireEvent.click(await screen.findByRole("button", { name: en["privacy.deleteAtMaktaba"] }));
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/reports/maktaba"));
    expect(await screen.findByTestId("reports-deleted")).toHaveTextContent(
      en["privacy.deleteAtMaktaba.done"].split(" (")[0]
    );
  });
});
