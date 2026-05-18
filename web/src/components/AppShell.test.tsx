// Story 11.10 — the SW-update affordance must be a REAL "reload to
// update" action, not a dead/mislabeled "Retry" toast. This proves the
// toast renders the localized copy plus a Reload button whose click
// activates the waiting SW (postMessage SKIP_WAITING).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post: vi.fn() } };
});

import { AppShell } from "./AppShell";
import { AuthProvider } from "../lib/auth";
import { I18nProvider } from "../lib/i18n";
import { ShortcutProvider } from "../lib/keyboard/shortcuts";
import { ToastProvider } from "@ds/components/Toast/Toast";
import en from "../i18n/en.json";
import type { SwUpdateDetail } from "../lib/pwa";

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
