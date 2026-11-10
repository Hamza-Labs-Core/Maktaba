import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueueSection, SettingsSection } from "./sections";
import { renderAt } from "../../../test/queueHarness";
import type { ManagedLibrary } from "../../../lib/libraryAdminApi";

const lib = (settings: Record<string, unknown>) =>
  ({ id: "l1", name: "Lectures", roots: [], settings }) as unknown as ManagedLibrary;

// Epic 59 story 59.02: a regular scan is said to keep the disks awake; Run now starts the
// library's held work.
describe("library page: quiet hours and disk sleep", () => {
  it("says a sweep keeps the disks awake, and nothing without one", () => {
    const noop = () => undefined;
    const { unmount } = renderAt(
      <SettingsSection
        lib={lib({ sweep_interval_sec: 21600 })}
        onEdit={noop}
        onMerge={noop}
        onRemove={noop}
        canMerge
      />,
      "/admin/libraries/l1"
    );
    expect(screen.getByText("A regular scan keeps the disks of this library awake.")).toBeTruthy();
    unmount();
    renderAt(
      <SettingsSection lib={lib({})} onEdit={noop} onMerge={noop} onRemove={noop} canMerge />,
      "/admin/libraries/l1"
    );
    expect(screen.queryByTestId("sweep-awake")).toBeNull();
  });

  it("offers Run now", async () => {
    const run = vi.fn();
    renderAt(
      <QueueSection
        h={null}
        stages={[]}
        activity={undefined}
        failures={null}
        onOffline={() => undefined}
        onRequeue={() => undefined}
        onRunNow={run}
      />,
      "/admin/libraries/l1"
    );
    await userEvent.click(screen.getByRole("button", { name: "Run now" }));
    expect(run).toHaveBeenCalledOnce();
  });
});
