// The Mac server app's permissions reminder: the walkthrough once, then a
// banner while a required permission is off, checked again when the window
// comes back; driven by a fake of the shell's commands (permissions.rs).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../../lib/i18n";
import {
  isLocalServerPage,
  isMissing,
  missingRequired,
  type PermissionItem,
  type PermissionsApi,
  type PermissionsReport,
} from "../../lib/macPermissions";
import { MacPermissionsReminder, resetReminder } from "./MacPermissionsReminder";

function item(
  id: PermissionItem["id"],
  state: PermissionItem["state"],
  required = true
): PermissionItem {
  const pane = {
    full_disk_access: "full_disk_access",
    local_network: "local_network",
    login_item: "login_items",
    keep_awake: "energy",
  } as const;
  return { id, state, required, pane: pane[id] };
}

function report(over: Partial<PermissionsReport> = {}): PermissionsReport {
  return {
    platform: "macos",
    server_mode: true,
    seen: true,
    items: [
      item("full_disk_access", "denied"),
      item("local_network", "unknown"),
      item("login_item", "granted"),
      item("keep_awake", "denied", false),
    ],
    ...over,
  };
}

function fakeApi(first: PermissionsReport) {
  let current = first;
  const api = {
    report: vi.fn(async () => current),
    openPane: vi.fn(async () => {}),
    markSeen: vi.fn(async () => {
      current = { ...current, seen: true };
    }),
    set(r: PermissionsReport) {
      current = r;
    },
  };
  return api as typeof api & PermissionsApi;
}

function renderReminder(api: PermissionsApi, enabled = true) {
  return render(
    <MemoryRouter>
      <I18nProvider>
        <MacPermissionsReminder api={api} enabled={enabled} />
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("MacPermissionsReminder", () => {
  beforeEach(() => {
    localStorage.clear();
    resetReminder();
  });
  afterEach(() => {
    document.documentElement.dir = "ltr";
  });

  it("shows the walkthrough once, then remembers it was shown", async () => {
    const api = fakeApi(report({ seen: false }));
    renderReminder(api);
    const dialog = await screen.findByRole("dialog", { name: "Permissions on this Mac" });
    expect(within(dialog).getByText("Full Disk Access")).toBeInTheDocument();
    expect(within(dialog).getByText("Local Network")).toBeInTheDocument();
    expect(within(dialog).getByText("Run in the background")).toBeInTheDocument();
    expect(within(dialog).getByText("Keep this Mac awake")).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: "Learn more" })).toHaveAttribute(
      "href",
      "/help/mac-app#permissions"
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Done" }));
    expect(api.markSeen).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // a required one is still off: the banner stays
    expect(await screen.findByTestId("perm-banner")).toHaveTextContent(
      "Maktaba needs a permission on this Mac: Full Disk Access."
    );
  });

  it("each item says why, its state, the steps, and opens its own pane", async () => {
    const api = fakeApi(report());
    renderReminder(api);
    await userEvent.click(await screen.findByRole("button", { name: "Fix" }));
    const fda = screen.getByTestId("perm-full_disk_access");
    expect(within(fda).getByText("Required")).toBeInTheDocument();
    expect(within(fda).getByTestId("perm-full_disk_access-state")).toHaveTextContent("Off");
    expect(within(fda).getByText(/click \+, choose Maktaba in Applications/)).toBeInTheDocument();
    expect(within(fda).getByText(/Pause server, then Resume server/)).toBeInTheDocument();
    await userEvent.click(within(fda).getByRole("button", { name: "Open System Settings" }));
    expect(api.openPane).toHaveBeenLastCalledWith("full_disk_access");

    const net = screen.getByTestId("perm-local_network");
    expect(within(net).getByTestId("perm-local_network-state")).toHaveTextContent("Cannot tell");
    await userEvent.click(within(net).getByRole("button", { name: "Open System Settings" }));
    expect(api.openPane).toHaveBeenLastCalledWith("local_network");

    // granted: no steps, no button
    const login = screen.getByTestId("perm-login_item");
    expect(within(login).getByTestId("perm-login_item-state")).toHaveTextContent("On");
    expect(within(login).queryByRole("button")).not.toBeInTheDocument();

    const awake = screen.getByTestId("perm-keep_awake");
    expect(within(awake).getByText("Optional")).toBeInTheDocument();
    expect(within(awake).getByTestId("perm-keep_awake-state")).toHaveTextContent(
      "Sleeps when idle"
    );
  });

  it("says so when System Settings does not open", async () => {
    const api = fakeApi(report());
    api.openPane.mockRejectedValueOnce(new Error("no"));
    renderReminder(api);
    await userEvent.click(await screen.findByRole("button", { name: "Fix" }));
    const fda = screen.getByTestId("perm-full_disk_access");
    await userEvent.click(within(fda).getByRole("button", { name: "Open System Settings" }));
    expect(await within(fda).findByRole("alert")).toHaveTextContent(/did not open/);
  });

  it("checks again when the window comes back, and the banner goes once it is on", async () => {
    const api = fakeApi(report());
    renderReminder(api);
    expect(await screen.findByTestId("perm-banner")).toBeInTheDocument();
    api.set(
      report({ items: [item("full_disk_access", "granted"), item("login_item", "granted")] })
    );
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
    });
    await waitFor(() => expect(screen.queryByTestId("perm-banner")).not.toBeInTheDocument());
    // and comes back if it is turned off again
    api.set(report());
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(await screen.findByTestId("perm-banner")).toBeInTheDocument();
    expect(api.report.mock.calls.length).toBeGreaterThanOrEqual(3);
  });

  it("Later hides the banner until the app opens again", async () => {
    renderReminder(fakeApi(report()));
    await userEvent.click(await screen.findByRole("button", { name: "Later" }));
    expect(screen.queryByTestId("perm-banner")).not.toBeInTheDocument();
  });

  it("no banner when only what macOS cannot tell, or something optional, is off", async () => {
    const api = fakeApi(
      report({
        items: [
          item("full_disk_access", "granted"),
          item("local_network", "unknown"),
          item("keep_awake", "denied", false),
        ],
      })
    );
    renderReminder(api);
    await waitFor(() => expect(api.report).toHaveBeenCalled());
    expect(screen.queryByTestId("perm-banner")).not.toBeInTheDocument();
  });

  it("nothing in client mode, off a Mac, with an older shell, or outside the app", async () => {
    const client = fakeApi(report({ server_mode: false, seen: false }));
    const { unmount } = renderReminder(client);
    await waitFor(() => expect(client.report).toHaveBeenCalled());
    expect(screen.queryByTestId("perm-banner")).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    unmount();

    const windows = fakeApi(report({ platform: "windows", items: [], seen: false }));
    renderReminder(windows).unmount();
    await waitFor(() => expect(windows.report).toHaveBeenCalled());

    const old = fakeApi(report());
    old.report.mockRejectedValue("command host_permissions not found");
    renderReminder(old).unmount();

    const browser = fakeApi(report({ seen: false }));
    renderReminder(browser, false);
    expect(browser.report).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(isLocalServerPage()).toBe(false);
  });

  it("speaks Arabic, right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderReminder(fakeApi(report()));
    expect(await screen.findByTestId("perm-banner")).toHaveTextContent(
      "تحتاج مكتبة إذنًا على هذا الماك: الوصول الكامل إلى القرص."
    );
    await userEvent.click(screen.getByRole("button", { name: "إصلاح" }));
    expect(screen.getAllByRole("button", { name: "فتح إعدادات النظام" }).length).toBeGreaterThan(0);
    expect(document.documentElement.dir).toBe("rtl");
  });
});

describe("macPermissions", () => {
  it("counts only a required item that is off or not set up", () => {
    expect(isMissing(item("full_disk_access", "denied"))).toBe(true);
    expect(isMissing(item("login_item", "not_asked"))).toBe(true);
    expect(isMissing(item("local_network", "unknown"))).toBe(false);
    expect(isMissing(item("full_disk_access", "granted"))).toBe(false);
    expect(isMissing(item("keep_awake", "denied", false))).toBe(false);
    expect(missingRequired(null)).toEqual([]);
    expect(missingRequired(report()).map((i) => i.id)).toEqual(["full_disk_access"]);
  });
});
