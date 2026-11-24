// Story 13.10: in the desktop app the shell's header is the window's title bar (one bar,
// not the system's above the app's): the drag region, the menu button and the controls.
import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

const { get, invoke } = vi.hoisted(() => ({ get: vi.fn(), invoke: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post: vi.fn() } };
});
vi.mock("@tauri-apps/api/core", () => ({ invoke }));
vi.mock("@tauri-apps/api/event", () => ({ listen: vi.fn(async () => () => {}) }));

import { AppShell } from "./AppShell";
import { DesktopFrame } from "./DesktopFrame";
import { AuthProvider } from "../lib/auth";
import { I18nProvider } from "../lib/i18n";
import { ShortcutProvider } from "../lib/keyboard/shortcuts";
import { ToastProvider } from "@ds/components/Toast/Toast";

function renderShell() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <I18nProvider>
        <ToastProvider>
          <DesktopFrame />
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

describe("AppShell as the desktop window's title bar", () => {
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
    invoke.mockReset();
  });

  it("is an ordinary header in a browser", async () => {
    get.mockRejectedValue(Object.assign(new Error("401"), { status: 401 }));
    renderShell();
    const header = await screen.findByRole("banner");
    expect(header).not.toHaveAttribute("data-tauri-drag-region");
    expect(within(header).queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("carries the drag region, the menu and the caption buttons when the shell gives it the frame", async () => {
    get.mockRejectedValue(Object.assign(new Error("401"), { status: 401 }));
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {};
    invoke.mockImplementation(async (cmd: string) =>
      cmd === "frame_claim"
        ? { controls: "windows", layout: "", side: "end", maximized: false, snapLayouts: true }
        : null
    );
    renderShell();
    const header = await screen.findByRole("banner");
    await waitFor(() => expect(header).toHaveAttribute("data-tauri-drag-region", ""));
    expect(within(header).getByRole("button", { name: "Menu" })).toBeInTheDocument();
    expect(within(header).getByRole("button", { name: "Close" })).toBeInTheDocument();
    // one bar: no slim bar above the header
    expect(screen.queryByTestId("frame-bar")).toBeNull();
  });
});
