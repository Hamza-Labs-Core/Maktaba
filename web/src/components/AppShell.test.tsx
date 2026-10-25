// Story 11.10 — the SW-update affordance must be a REAL "reload to
// update" action, not a dead/mislabeled "Retry" toast. This proves the
// toast renders the localized copy plus a Reload button whose click
// activates the waiting SW (postMessage SKIP_WAITING).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post: vi.fn() } };
});

import { AppShell, MoreMenu } from "./AppShell";
import { AuthProvider } from "../lib/auth";
import { I18nProvider } from "../lib/i18n";
import { ShortcutProvider } from "../lib/keyboard/shortcuts";
import { ToastProvider } from "@ds/components/Toast/Toast";
import en from "../i18n/en.json";
import type { SwUpdateDetail } from "../lib/pwa";
import { resetUpdateStatusCache } from "../lib/updates";
import { STATUS_PATH, resetStatusStore } from "../lib/systemStatus";

function renderShell(path = "/") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <I18nProvider>
        <ToastProvider>
          <AuthProvider>
            <ShortcutProvider>
              <AppShell />
            </ShortcutProvider>
          </AuthProvider>
        </ToastProvider>
      </I18nProvider>
    </MemoryRouter>
  );
}

describe("AppShell SW-update affordance", () => {
  beforeEach(() => {
    // No existing session: /me 401s, SPA boots logged-out.
    get.mockRejectedValue(Object.assign(new Error("401"), { status: 401 }));
  });

  it("shows the real update copy + a Reload button (not the old 'Retry')", async () => {
    renderShell();
    await act(async () => {
      window.dispatchEvent(
        new CustomEvent<SwUpdateDetail>("mkt:sw-update", {
          detail: { registration: { waiting: { postMessage: vi.fn() } } as never },
        })
      );
    });

    expect(screen.getByText(en["pwa.updateAvailable"])).toBeInTheDocument();
    expect(screen.getByRole("button", { name: en["pwa.reload"] })).toBeInTheDocument();
    // The old mislabeled affordance used common.retry — must be gone.
    expect(screen.queryByText(en["common.retry"])).not.toBeInTheDocument();
  });

  it("clicking Reload posts SKIP_WAITING to the waiting service worker", async () => {
    const postMessage = vi.fn();
    renderShell();
    await act(async () => {
      window.dispatchEvent(
        new CustomEvent<SwUpdateDetail>("mkt:sw-update", {
          detail: { registration: { waiting: { postMessage } } as never },
        })
      );
    });

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: en["pwa.reload"] }));
    expect(postMessage).toHaveBeenCalledWith({ type: "SKIP_WAITING" });
  });
});

describe("AppShell admin navigation", () => {
  it("links the Extensions page for admins", async () => {
    get.mockResolvedValue({
      user: { id: "u1", username: "root", is_admin: true, roles: [] },
    });
    renderShell();
    const link = await screen.findByRole("link", { name: en["nav.admin.extensions"] });
    expect(link).toHaveAttribute("href", "/admin/extensions");
  });

  it("links TV changes, and only it is current on the Changes page", async () => {
    get.mockResolvedValue({
      user: { id: "u1", username: "root", is_admin: true, roles: [] },
    });
    renderShell("/admin/live-tv/changes");
    const changes = await screen.findByRole("link", { name: en["nav.admin.tvChanges"] });
    expect(changes).toHaveAttribute("href", "/admin/live-tv/changes");
    expect(changes).toHaveAttribute("aria-current", "page");
    const liveTv = screen.getByRole("link", { name: en["nav.admin.liveTv"] });
    expect(liveTv).not.toHaveAttribute("aria-current");
  });
});

describe("AppShell server pages (Processing, Transcription, Settings)", () => {
  const viewer = { user: { id: "u2", username: "viewer", is_admin: false, roles: [] } };
  const admin = { user: { id: "u1", username: "root", is_admin: true, roles: [] } };
  const serverPages = [en["nav.queue"], en["nav.transcription"], en["nav.settings"]];

  function renderMore() {
    return render(
      <MemoryRouter>
        <I18nProvider>
          <AuthProvider>
            <MoreMenu />
          </AuthProvider>
        </I18nProvider>
      </MemoryRouter>
    );
  }

  it("hides them from a viewer's sidebar and five-item tab bar", async () => {
    get.mockResolvedValue(viewer);
    renderShell();
    const [sidebar, tabbar] = await screen.findAllByRole("navigation", {
      name: en["nav.primary"],
    });
    await within(sidebar).findByRole("link", { name: en["nav.account"] });
    for (const name of serverPages) {
      expect(within(sidebar).queryByRole("link", { name })).not.toBeInTheDocument();
      expect(within(tabbar).queryByRole("link", { name })).not.toBeInTheDocument();
    }
    const tabs = within(tabbar).getAllByRole("link");
    expect(tabs.map((a) => a.getAttribute("href"))).toEqual([
      "/library",
      "/series",
      "/guide",
      "/search",
      "/more",
    ]);
  });

  it("lists them for an admin in the sidebar, not on the tab bar", async () => {
    get.mockResolvedValue(admin);
    renderShell();
    const [sidebar, tabbar] = await screen.findAllByRole("navigation", {
      name: en["nav.primary"],
    });
    expect(
      await within(sidebar).findByRole("link", { name: en["nav.transcription"] })
    ).toHaveAttribute("href", "/transcription");
    expect(within(sidebar).getByRole("link", { name: en["nav.queue"] })).toHaveAttribute(
      "href",
      "/queue"
    );
    expect(within(sidebar).getByRole("link", { name: en["nav.settings"] })).toHaveAttribute(
      "href",
      "/settings"
    );
    expect(within(tabbar).getAllByRole("link")).toHaveLength(5);
  });

  it("keeps them off a viewer's More page", async () => {
    get.mockResolvedValue(viewer);
    renderMore();
    await screen.findByRole("link", { name: en["nav.account"] });
    for (const name of serverPages) {
      expect(screen.queryByRole("link", { name })).not.toBeInTheDocument();
    }
  });

  it("puts Transcription on an admin's More page", async () => {
    get.mockResolvedValue(admin);
    renderMore();
    expect(await screen.findByRole("link", { name: en["nav.transcription"] })).toHaveAttribute(
      "href",
      "/transcription"
    );
  });
});

describe("AppShell System health badge", () => {
  const admin = { user: { id: "u1", username: "root", is_admin: true, roles: [] } };
  const viewer = { user: { id: "u2", username: "viewer", is_admin: false, roles: [] } };
  beforeEach(() => {
    resetUpdateStatusCache();
    resetStatusStore();
  });

  function serve(user: unknown, status: string) {
    get.mockImplementation((url: string) =>
      url.startsWith(STATUS_PATH)
        ? Promise.resolve({ status, items: [], checked_at: "", code: "", reason: "" })
        : url.startsWith("/api/system/updates")
          ? Promise.reject(new Error("off"))
          : Promise.resolve(user)
    );
  }

  it("shows a red dot on System health and More while the system is down", async () => {
    serve(admin, "down");
    renderShell();
    const [sidebar, tabbar] = await screen.findAllByRole("navigation", { name: en["nav.primary"] });
    const health = await within(sidebar).findByRole("link", { name: en["nav.admin.health"] });
    await waitFor(() => expect(within(health).getByTestId("health-badge")).toBeInTheDocument());
    expect(health).toHaveAccessibleDescription(en["health.navDown"]);
    const more = within(tabbar).getByRole("link", { name: en["nav.more"] });
    expect(within(more).getByTestId("health-badge")).toBeInTheDocument();
  });

  it("shows no dot when the system is only degraded, and never asks for a viewer", async () => {
    serve(admin, "degraded");
    const { unmount } = renderShell();
    await screen.findByRole("link", { name: en["nav.admin.health"] });
    await waitFor(() => expect(get).toHaveBeenCalledWith(STATUS_PATH));
    expect(screen.queryByTestId("health-badge")).not.toBeInTheDocument();
    unmount();
    get.mockClear();
    resetStatusStore();
    serve(viewer, "down");
    renderShell();
    await screen.findAllByRole("link", { name: en["nav.account"] });
    expect(screen.queryByTestId("health-badge")).not.toBeInTheDocument();
    expect(get).not.toHaveBeenCalledWith(STATUS_PATH);
  });
});

describe("AppShell update badge (Story 28.6)", () => {
  const viewer = { user: { id: "u2", username: "viewer", is_admin: false, roles: [] } };
  const admin = { user: { id: "u1", username: "root", is_admin: true, roles: [] } };
  const newer = {
    available: true,
    latest_version: "v2.0.0",
    current_version: "v1.0.0",
    channel: "stable",
    checked_at: "",
  };
  beforeEach(() => {
    resetUpdateStatusCache();
    resetStatusStore();
  });

  function serve(user: unknown, updates: unknown) {
    get.mockImplementation((url: string) =>
      url.startsWith(STATUS_PATH)
        ? Promise.resolve({ status: "ok", items: [], checked_at: "", code: "", reason: "" })
        : Promise.resolve(url.startsWith("/api/system/updates") ? updates : user)
    );
  }

  it("badges an admin's Settings (Administration group) and More when an update is available", async () => {
    serve(admin, newer);
    renderShell();
    const [sidebar, tabbar] = await screen.findAllByRole("navigation", { name: en["nav.primary"] });
    const settings = await within(sidebar).findByRole("link", { name: en["nav.settings"] });
    await waitFor(() => expect(settings).toHaveAccessibleDescription(en["update.badge"]));
    expect(settings).toHaveAttribute("href", "/settings");
    expect(within(settings).getByTestId("update-badge")).toBeInTheDocument();
    const more = within(tabbar).getByRole("link", { name: en["nav.more"] });
    expect(more).toHaveAttribute("href", "/more");
    expect(more).toHaveAccessibleDescription(en["update.badge"]);
    expect(get).toHaveBeenCalledWith("/api/system/updates");
  });

  it("badges only More for a viewer, who has no Settings link", async () => {
    serve(viewer, newer);
    renderShell();
    const [sidebar, tabbar] = await screen.findAllByRole("navigation", { name: en["nav.primary"] });
    const more = within(tabbar).getByRole("link", { name: en["nav.more"] });
    await waitFor(() => expect(more).toHaveAccessibleDescription(en["update.badge"]));
    expect(within(more).getByTestId("update-badge")).toBeInTheDocument();
    expect(
      within(sidebar).queryByRole("link", { name: en["nav.settings"] })
    ).not.toBeInTheDocument();
    expect(screen.getAllByTestId("update-badge")).toHaveLength(1);
  });

  it("shows no badge when up to date or when checks are off", async () => {
    for (const status of [
      { available: false, current_version: "v1.0.0", channel: "stable", checked_at: "" },
      { ...newer, disabled: true },
    ]) {
      resetUpdateStatusCache();
      serve(admin, status);
      const { unmount } = renderShell();
      await screen.findAllByRole("link", { name: en["nav.settings"] });
      await waitFor(() => expect(get).toHaveBeenCalledWith("/api/system/updates"));
      expect(screen.queryByTestId("update-badge")).not.toBeInTheDocument();
      unmount();
    }
  });
});
