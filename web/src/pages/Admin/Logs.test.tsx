// Admin log viewer: proves it polls /api/admin/logs/stream, renders the
// structured lines colour-coded by level, applies the level filter to
// the query, and triggers the diagnostics-bundle download on export.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, downloadBlob } = vi.hoisted(() => ({ get: vi.fn(), downloadBlob: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get }, downloadBlob };
});
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ user: { username: "admin", is_admin: true } }),
}));

import { AdminLogs } from "./Logs";

function renderLogs() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <AdminLogs />
      </ToastProvider>
    </I18nProvider>
  );
}

describe("AdminLogs", () => {
  beforeEach(() => {
    get.mockResolvedValue({
      entries: [
        { ts: "2026-06-10T00:00:00Z", level: "info", service: "api", msg: "started" },
        { ts: "2026-06-10T00:00:01Z", level: "error", service: "streaming", msg: "boom", err: "x" },
      ],
      count: 2,
    });
    downloadBlob.mockResolvedValue(undefined);
  });
  afterEach(() => {
    vi.clearAllMocks();
    window.history.replaceState({}, "", "/");
  });

  // a failure elsewhere links here with its machine and its time (update-attempts.md)
  it("opens with the filters its address gives, the time window client-side", async () => {
    get.mockResolvedValue({
      entries: [
        { ts: "2026-10-24T05:20:00Z", level: "error", service: "api", msg: "before it" },
        { ts: "2026-10-24T05:30:00Z", level: "error", service: "api", msg: "the failure" },
        { ts: "2026-10-24T05:40:00Z", level: "error", service: "api", msg: "after it" },
      ],
      count: 3,
    });
    window.history.replaceState(
      {},
      "",
      "/admin/logs?q=studio&services=api&level=warn&since=2026-10-24T05:28:00Z&until=2026-10-24T05:33:00Z"
    );
    const user = userEvent.setup();
    renderLogs();
    await waitFor(() => expect(get).toHaveBeenCalled());
    const url = String(get.mock.calls[0][0]);
    expect(url).toContain("level=warn");
    expect(url).toContain("services=api");
    expect(url).toContain("q=studio");
    expect(await screen.findByText("the failure")).toBeInTheDocument();
    expect(screen.queryByText("before it")).toBeNull();
    expect(screen.queryByText("after it")).toBeNull();
    expect(screen.getByTestId("logs-window")).toHaveTextContent("Showing the lines from");
    expect(screen.getByDisplayValue("studio")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Show all" }));
    expect(await screen.findByText("before it")).toBeInTheDocument();
    expect(screen.getByText("after it")).toBeInTheDocument();
    expect(screen.queryByTestId("logs-window")).toBeNull();
  });

  it("says when the window's lines are no longer in the live logs", async () => {
    window.history.replaceState(
      {},
      "",
      "/admin/logs?since=2020-01-01T00:00:00Z&until=2020-01-01T00:05:00Z"
    );
    renderLogs();
    expect(
      await screen.findByText(/None of the lines of this time are left in the live logs/)
    ).toBeInTheDocument();
  });

  it("polls the stream endpoint and renders entries", async () => {
    renderLogs();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(get.mock.calls[0][0]).toContain("/api/admin/logs/stream");
    await waitFor(() => {
      expect(screen.getByText("started")).toBeInTheDocument();
      expect(screen.getByText("boom")).toBeInTheDocument();
    });
  });

  it("exports a diagnostics bundle on click", async () => {
    const user = userEvent.setup();
    renderLogs();
    await waitFor(() => expect(get).toHaveBeenCalled());
    await user.click(screen.getByRole("button", { name: /Export bundle/i }));
    await waitFor(() =>
      expect(downloadBlob).toHaveBeenCalledWith(
        expect.stringContaining("/api/admin/logs/export"),
        expect.stringContaining(".tar.gz")
      )
    );
  });
});
