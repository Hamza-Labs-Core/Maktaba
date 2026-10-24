// Admin log viewer: proves it polls /api/admin/logs/stream, renders the
// structured lines colour-coded by level, applies the level filter to
// the query, and triggers the diagnostics-bundle download on export.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
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
  it("opens with the filters its address gives, the time window sent to the server", async () => {
    get.mockResolvedValue({
      entries: [
        { ts: "2026-10-24T05:20:00Z", level: "error", service: "api", msg: "before it" },
        { ts: "2026-10-24T05:30:00Z", level: "error", service: "api", msg: "the failure" },
        { ts: "2026-10-24T05:40:00Z", level: "error", service: "api", msg: "after it" },
      ],
      count: 3,
    });
    // an old desktop app's link searched for its version (q=), which hid the failure: not now
    window.history.replaceState(
      {},
      "",
      "/admin/logs?q=0.3.0&services=api&level=warn&since=2026-10-24T05:28:00Z&until=2026-10-24T05:33:00Z"
    );
    const user = userEvent.setup();
    renderLogs();
    await waitFor(() => expect(get).toHaveBeenCalled());
    const url = String(get.mock.calls.map(([u]) => String(u)).find((u) => u.includes("/stream")));
    expect(url).toContain("level=warn");
    expect(url).toContain("services=api");
    expect(url).toContain("since=2026-10-24T05%3A28%3A00Z");
    expect(url).toContain("until=2026-10-24T05%3A33%3A00Z");
    expect(url).not.toContain("q=");
    expect(await screen.findByText("the failure")).toBeInTheDocument();
    expect(screen.queryByText("before it")).toBeNull();
    expect(screen.queryByText("after it")).toBeNull();
    expect(screen.getByTestId("logs-window")).toHaveTextContent("Showing the lines from");
    expect(screen.queryByDisplayValue("0.3.0")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Show all" }));
    expect(await screen.findByText("before it")).toBeInTheDocument();
    expect(screen.getByText("after it")).toBeInTheDocument();
    expect(screen.queryByTestId("logs-window")).toBeNull();
  });

  it("says when the window has no lines, and offers that window's logs bundle", async () => {
    window.history.replaceState(
      {},
      "",
      "/admin/logs?since=2020-01-01T00:00:00Z&until=2020-01-01T00:05:00Z"
    );
    const user = userEvent.setup();
    renderLogs();
    expect(
      await screen.findByText(
        "No lines from this time are in the live logs or in the machine's log files."
      )
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Download these logs" }));
    await waitFor(() =>
      expect(downloadBlob).toHaveBeenCalledWith(
        expect.stringContaining(
          "/api/admin/logs/export?level=debug&since=2020-01-01T00%3A00%3A00Z&until=2020-01-01T00%3A05%3A00Z"
        ),
        "maktaba-diagnostics.tar.gz"
      )
    );
  });

  // update-attempts.md: the update's story is in the machine's log files (server.log, the
  // desktop app's app.log, launcher.log), not only in the live logs
  it("shows the machine's log files of the window with the live logs, marked by file", async () => {
    get.mockImplementation((url: string) => {
      if (url.startsWith("/api/admin/logs/files")) {
        return Promise.resolve({
          node: "",
          entries: [
            {
              ts: "2026-10-24T05:29:00Z",
              level: "",
              service: "app",
              file: "app.log",
              msg: "update 0.3.0-rc.5 -> 0.3.0: installed, relaunching",
              raw: "2026-10-24T05:29:00Z update 0.3.0-rc.5 -> 0.3.0: installed, relaunching",
            },
            // the api's own line, also in the live logs: shown once
            {
              ts: "2026-10-24T05:30:00Z",
              level: "error",
              service: "api",
              file: "server.log",
              msg: "the failure",
              raw: '{"ts":"2026-10-24T05:30:00Z","msg":"the failure"}',
            },
            {
              ts: "2026-10-24T05:31:00Z",
              level: "",
              service: "server",
              file: "server.log",
              msg: "[serve] Codesigning failure loading plist",
              raw: "[serve] Codesigning failure loading plist",
            },
          ],
          files: [
            { name: "server.log" },
            { name: "app.log" },
            { name: "launcher.log", missing: true },
          ],
        });
      }
      return Promise.resolve({
        entries: [
          { ts: "2026-10-24T05:30:00Z", level: "error", service: "api", msg: "the failure" },
        ],
        count: 1,
      });
    });
    window.history.replaceState(
      {},
      "",
      "/admin/logs?since=2026-10-24T05:28:00Z&until=2026-10-24T05:33:00Z"
    );
    renderLogs();
    expect(
      await screen.findByText("[serve] Codesigning failure loading plist")
    ).toBeInTheDocument();
    const files = get.mock.calls.map(([u]) => String(u)).find((u) => u.includes("/files"));
    expect(files).toBe(
      "/api/admin/logs/files?since=2026-10-24T05%3A28%3A00Z&until=2026-10-24T05%3A33%3A00Z&limit=2000"
    );
    const msgs = Array.from(document.querySelectorAll(".mkt-logs__msg")).map((e) => e.textContent);
    expect(msgs).toEqual([
      "update 0.3.0-rc.5 -> 0.3.0: installed, relaunching",
      "the failure",
      "[serve] Codesigning failure loading plist",
    ]);
    const rows = Array.from(document.querySelectorAll("li.mkt-logs__row"));
    expect(rows[0]).toHaveTextContent("app · app.log");
    expect(rows[2]).toHaveTextContent("server · server.log");
  });

  it("shows a joined machine's own log files with ?node=, and says when it is away", async () => {
    let offline = false;
    get.mockImplementation((url: string) => {
      if (url === "/api/cluster") {
        return Promise.resolve({ nodes: [{ id: "n1", name: "Studio Mac" }] });
      }
      if (url.startsWith("/api/admin/logs/files")) {
        if (offline) {
          return Promise.reject(
            new ApiError({ type: "about:blank", title: "x", status: 503, code: "node_offline" })
          );
        }
        return Promise.resolve({
          node: "n1",
          entries: [
            {
              ts: "2026-10-24T05:30:00Z",
              level: "",
              service: "launcher",
              file: "launcher.log",
              msg: "boot_loop 0.3.0",
              raw: "{}",
            },
          ],
          files: [],
        });
      }
      return Promise.reject(new Error("unexpected " + url));
    });
    window.history.replaceState({}, "", "/admin/logs?node=n1&since=2026-10-24T05:28:00Z");
    const first = renderLogs();
    expect(await screen.findByText("boot_loop 0.3.0")).toBeInTheDocument();
    expect(await screen.findByTestId("logs-node")).toHaveTextContent("The log files of Studio Mac");
    const calls = get.mock.calls.map(([u]) => String(u));
    // the live logs are the home's: not shown for a machine
    expect(calls.some((u) => u.includes("/stream"))).toBe(false);
    expect(calls).toContain(
      "/api/admin/logs/files?since=2026-10-24T05%3A28%3A00Z&node=n1&limit=2000"
    );
    first.unmount();

    offline = true;
    renderLogs();
    expect(await screen.findByTestId("logs-files-error")).toHaveTextContent(
      "This machine is not connected to the home"
    );
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
