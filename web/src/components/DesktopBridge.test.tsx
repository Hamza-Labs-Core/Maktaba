import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, waitFor, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import type { MenuAction } from "../lib/desktop";

// Hoisted mock of the desktop bridge layer so we can drive menu actions
// deterministically without a Tauri runtime.
const mocks = vi.hoisted(() => ({
  registerMenuRouter: vi.fn(),
  registerDropHandler: vi.fn(),
}));

vi.mock("../lib/desktop", async () => {
  const actual = await vi.importActual<typeof import("../lib/desktop")>("../lib/desktop");
  return {
    ...actual,
    registerMenuRouter: mocks.registerMenuRouter,
    registerDropHandler: mocks.registerDropHandler,
  };
});

import { DesktopBridge } from "./DesktopBridge";

function LocationDisplay() {
  const loc = useLocation();
  return <div data-testid="location-display">{loc.pathname}</div>;
}

function renderBridge(initial = "/library") {
  return render(
    <MemoryRouter initialEntries={[initial]}>
      <DesktopBridge />
      <LocationDisplay />
    </MemoryRouter>
  );
}

describe("DesktopBridge", () => {
  beforeEach(() => {
    mocks.registerMenuRouter.mockReset();
    mocks.registerDropHandler.mockReset();
    mocks.registerDropHandler.mockResolvedValue(() => {});
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("subscribes to menu + drop events on mount and disposes on unmount", async () => {
    const menuDispose = vi.fn();
    const dropDispose = vi.fn();
    mocks.registerMenuRouter.mockResolvedValue(menuDispose);
    mocks.registerDropHandler.mockResolvedValue(dropDispose);

    const { unmount } = renderBridge();
    await waitFor(() => expect(mocks.registerMenuRouter).toHaveBeenCalled());
    await waitFor(() => expect(mocks.registerDropHandler).toHaveBeenCalled());

    unmount();
    await waitFor(() => expect(menuDispose).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(dropDispose).toHaveBeenCalledTimes(1));
  });

  it("navigates when a navigate menu action arrives", async () => {
    let dispatch!: (a: MenuAction) => void;
    mocks.registerMenuRouter.mockImplementation(async (cb: (a: MenuAction) => void) => {
      dispatch = cb;
      return () => {};
    });

    renderBridge("/library");
    await waitFor(() => expect(dispatch).toBeDefined());
    expect(screen.getByTestId("location-display")).toHaveTextContent("/library");

    dispatch({ kind: "navigate", to: "/settings" });
    await waitFor(() =>
      expect(screen.getByTestId("location-display")).toHaveTextContent("/settings")
    );
  });

  it("emits a maktaba:scan DOM event for the scan action", async () => {
    let dispatch!: (a: MenuAction) => void;
    mocks.registerMenuRouter.mockImplementation(async (cb: (a: MenuAction) => void) => {
      dispatch = cb;
      return () => {};
    });
    const onScan = vi.fn();
    window.addEventListener("maktaba:scan", onScan);

    renderBridge();
    await waitFor(() => expect(dispatch).toBeDefined());
    dispatch({ kind: "scan" });

    await waitFor(() => expect(onScan).toHaveBeenCalledTimes(1));
    window.removeEventListener("maktaba:scan", onScan);
  });

  it("emits maktaba:open-server-picker for the switch-server action", async () => {
    let dispatch!: (a: MenuAction) => void;
    mocks.registerMenuRouter.mockImplementation(async (cb: (a: MenuAction) => void) => {
      dispatch = cb;
      return () => {};
    });
    const onPicker = vi.fn();
    window.addEventListener("maktaba:open-server-picker", onPicker);

    renderBridge();
    await waitFor(() => expect(dispatch).toBeDefined());
    dispatch({ kind: "open-server-picker" });

    await waitFor(() => expect(onPicker).toHaveBeenCalledTimes(1));
    window.removeEventListener("maktaba:open-server-picker", onPicker);
  });
});
