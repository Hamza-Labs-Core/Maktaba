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

function renderShell() {
  return render(
    <MemoryRouter>
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
});

describe("AppShell transcription navigation", () => {
  const viewer = { user: { id: "u2", username: "viewer", is_admin: false, roles: [] } };

  it("lists Transcription in the sidebar, behind More on the six-item tab bar", async () => {
    get.mockResolvedValue(viewer);
    renderShell();
    const [sidebar, tabbar] = await screen.findAllByRole("navigation", {
      name: en["nav.primary"],
    });
    expect(within(sidebar).getByRole("link", { name: en["nav.transcription"] })).toHaveAttribute(
      "href",
      "/transcription"
    );
    const tabs = within(tabbar).getAllByRole("link");
    expect(tabs).toHaveLength(6);
    expect(tabs.map((a) => a.getAttribute("href"))).not.toContain("/transcription");
    expect(tabs[tabs.length - 1]).toHaveAttribute("href", "/more");
  });

  it("puts Transcription on the More page", async () => {
    get.mockResolvedValue(viewer);
    render(
      <MemoryRouter>
        <I18nProvider>
          <AuthProvider>
            <MoreMenu />
          </AuthProvider>
        </I18nProvider>
      </MemoryRouter>
    );
    expect(await screen.findByRole("link", { name: en["nav.transcription"] })).toHaveAttribute(
      "href",
      "/transcription"
    );
  });
});

describe("AppShell update badge (Story 28.6)", () => {
  const viewer = { user: { id: "u2", username: "viewer", is_admin: false, roles: [] } };
  beforeEach(() => resetUpdateStatusCache());

  function serve(updates: unknown) {
    get.mockImplementation((url: string) =>
      Promise.resolve(url.startsWith("/api/system/updates") ? updates : viewer)
    );
  }

  it("badges Settings and More when an update is available", async () => {
    serve({
      available: true,
      latest_version: "v2.0.0",
      current_version: "v1.0.0",
      channel: "stable",
      checked_at: "",
    });
    renderShell();
    const [sidebar, tabbar] = await screen.findAllByRole("navigation", { name: en["nav.primary"] });
    const settings = within(sidebar).getByRole("link", { name: en["nav.settings"] });
    await waitFor(() => expect(settings).toHaveAccessibleDescription(en["update.badge"]));
    expect(settings).toHaveAttribute("href", "/settings");
    expect(within(settings).getByTestId("update-badge")).toBeInTheDocument();
    const more = within(tabbar).getByRole("link", { name: en["nav.more"] });
    expect(more).toHaveAttribute("href", "/more");
    expect(more).toHaveAccessibleDescription(en["update.badge"]);
    expect(get).toHaveBeenCalledWith("/api/system/updates");
  });

  it("shows no badge when up to date or when checks are off", async () => {
    for (const status of [
      { available: false, current_version: "v1.0.0", channel: "stable", checked_at: "" },
      {
        available: true,
        disabled: true,
        latest_version: "v2.0.0",
        current_version: "v1.0.0",
        channel: "stable",
        checked_at: "",
      },
    ]) {
      resetUpdateStatusCache();
      serve(status);
      const { unmount } = renderShell();
      await screen.findAllByRole("link", { name: en["nav.transcription"] });
      expect(screen.queryByTestId("update-badge")).not.toBeInTheDocument();
      unmount();
    }
  });
});
