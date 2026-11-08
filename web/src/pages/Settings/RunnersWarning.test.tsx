// Epic 54: the Plugins and Updates pages say which plugin runner failed, on which machine,
// and the stages set to run as plugins that run in the worker meanwhile; nothing when
// System health's plugin_runners item is ok or System health cannot be read.
import { describe, expect, it, vi, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import { failedRunners, RunnersWarning } from "./RunnersWarning";
import type { StatusItem, StatusReport } from "../../lib/systemStatus";

const fetchStatus = vi.fn<() => Promise<StatusReport | null>>();
vi.mock("../../lib/systemStatus", () => ({ fetchStatus: () => fetchStatus() }));

const item = (status: StatusItem["status"], failed: unknown): StatusItem => ({
  id: "plugin_runners",
  group: "services",
  kind: "plugin_runners",
  name: "Plugin runners",
  status,
  code: status === "ok" ? "plugin_runners_ok" : "plugin_runners_failed",
  reason: "",
  last_checked: "2026-11-08T12:00:00Z",
  help: "plugin_runners",
  details: { failed },
});
const report = (items: StatusItem[]): StatusReport => ({
  status: "degraded",
  code: "",
  reason: "",
  checked_at: "2026-11-08T12:00:00Z",
  items,
  processes_supervised: true,
});

afterEach(() => fetchStatus.mockReset());

describe("failedRunners", () => {
  it("reads the failed runners of a plugin_runners item that is not ok", () => {
    const f = [{ machine: "home", runner: "net", stages: ["books"] }];
    expect(failedRunners([item("degraded", f)])).toEqual(f);
    expect(failedRunners([item("ok", [])])).toEqual([]);
    expect(failedRunners(undefined)).toEqual([]);
    expect(failedRunners([item("degraded", "garbled")])).toEqual([]);
  });
});

describe("RunnersWarning", () => {
  const show = () =>
    render(
      <I18nProvider>
        <MemoryRouter>
          <RunnersWarning />
        </MemoryRouter>
      </I18nProvider>
    );

  it("names each failed runner, its machine and its stages, and links to Components", async () => {
    fetchStatus.mockResolvedValue(
      report([
        item("degraded", [
          { machine: "home", runner: "net", stages: ["books"] },
          { machine: "node-b", runner: "light", stages: ["detect", "tag"] },
        ]),
      ])
    );
    show();
    const home = await screen.findByTestId("runners-warning-home-net");
    expect(home.textContent).toContain("this home");
    expect(home.textContent).toContain("(1)");
    expect(home.textContent).toContain("(books)");
    expect(screen.getByTestId("runners-warning-node-b-light").textContent).toContain(
      "(detect, tag)"
    );
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Components" }).getAttribute("href")).toBe(
      "/settings/components"
    );
  });

  it("shows nothing when every runner runs, or System health cannot be read", async () => {
    fetchStatus.mockResolvedValue(report([item("ok", [])]));
    const { container, unmount } = show();
    await vi.waitFor(() => expect(fetchStatus).toHaveBeenCalled());
    expect(container.textContent).toBe("");
    unmount();
    fetchStatus.mockResolvedValue(null);
    const again = show();
    await vi.waitFor(() => expect(fetchStatus).toHaveBeenCalledTimes(2));
    expect(again.container.textContent).toBe("");
  });
});
