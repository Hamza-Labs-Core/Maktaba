// Story 13.12: the tray panel's bridge to the shell (lib/trayPanel.ts).
import { afterEach, describe, expect, it, vi } from "vitest";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));

import { chooseTrayItem, hideTrayPanel, isTrayPanel, trayPanelState } from "./trayPanel";

type W = Record<string, unknown>;

describe("the tray panel's bridge", () => {
  afterEach(() => {
    invoke.mockReset();
    for (const k of ["__TAURI_INTERNALS__", "__MAKTABA_PANEL__", "__MAKTABA_TRAY_PREVIEW__"])
      delete (window as unknown as W)[k];
  });

  it("is the panel only when the shell marked the page", () => {
    expect(isTrayPanel()).toBe(false);
    (window as unknown as W).__MAKTABA_PANEL__ = "tray";
    expect(isTrayPanel()).toBe(true);
  });

  it("asks the shell, and nothing outside the desktop app", async () => {
    expect(await trayPanelState()).toBeNull();
    expect(invoke).not.toHaveBeenCalled();
    (window as unknown as W).__TAURI_INTERNALS__ = {};
    invoke.mockResolvedValueOnce({ lang: "en", tone: "ok", entries: [] });
    expect(await trayPanelState()).toEqual({ lang: "en", tone: "ok", entries: [] });
    invoke.mockResolvedValueOnce("nonsense");
    expect(await trayPanelState()).toBeNull();
    invoke.mockResolvedValue(null);
    await chooseTrayItem("show");
    expect(invoke).toHaveBeenLastCalledWith("tray_panel_choose", { id: "show" });
    await hideTrayPanel();
    expect(invoke).toHaveBeenLastCalledWith("tray_panel_hide", undefined);
  });

  it("draws a preview without the shell, for the visual references", async () => {
    const state = { lang: "ar", tone: "ok", entries: [] };
    (window as unknown as W).__MAKTABA_TRAY_PREVIEW__ = state;
    expect(await trayPanelState()).toBe(state);
    await chooseTrayItem("show");
    expect(invoke).not.toHaveBeenCalled();
  });
});
