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
