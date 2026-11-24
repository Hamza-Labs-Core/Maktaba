// Story 13.10: the page's side of the desktop app's own frame (lib/frame.ts).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));

import { chooseFrameMenu, claimFrame, frameAction, frameMenuItems, gtkButtons } from "./frame";

type W = Record<string, unknown>;

describe("gtkButtons", () => {
  it("reads GNOME's default, and an empty or unknown setting as it", () => {
    expect(gtkButtons("appmenu:close")).toEqual({ start: [], end: ["close"] });
    expect(gtkButtons("")).toEqual({ start: [], end: ["close"] });
    expect(gtkButtons("icon,menu:")).toEqual({ start: [], end: ["close"] });
  });

  it("keeps the order and the side of each button", () => {
    expect(gtkButtons(":minimize,maximize,close")).toEqual({
      start: [],
      end: ["minimize", "maximize", "close"],
    });
    expect(gtkButtons("close,minimize:appmenu")).toEqual({ start: ["close", "minimize"], end: [] });
  });
});

describe("the shell's frame commands", () => {
  beforeEach(() => {
    invoke.mockReset();
  });
  afterEach(() => {
    delete (window as unknown as W).__TAURI_INTERNALS__;
    delete (window as unknown as W).__MAKTABA_FRAME_PREVIEW__;
  });

  it("claims nothing outside the desktop app", async () => {
    expect(await claimFrame()).toBeNull();
    await frameAction("close");
    expect(invoke).not.toHaveBeenCalled();
  });

  it("claims the frame from the shell and keeps the system's when it refuses", async () => {
    (window as unknown as W).__TAURI_INTERNALS__ = {};
    invoke.mockResolvedValueOnce({
      controls: "windows",
      layout: "",
      side: "end",
      maximized: true,
      snapLayouts: true,
    });
    expect(await claimFrame()).toMatchObject({ controls: "windows", maximized: true });
    expect(invoke).toHaveBeenCalledWith("frame_claim", undefined);

    invoke.mockRejectedValueOnce(new Error("not allowed"));
    expect(await claimFrame()).toBeNull();
    invoke.mockResolvedValueOnce({ controls: "toaster" });
    expect(await claimFrame()).toBeNull();
  });

  it("sends the caption buttons, the menu's list and its choice", async () => {
    (window as unknown as W).__TAURI_INTERNALS__ = {};
    invoke.mockResolvedValue(null);
    await frameAction("snap");
    expect(invoke).toHaveBeenLastCalledWith("frame_action", { action: "snap" });
    await chooseFrameMenu("preferences");
    expect(invoke).toHaveBeenLastCalledWith("frame_menu", { id: "preferences" });
    invoke.mockResolvedValueOnce([{ title: "File", items: [] }]);
    expect(await frameMenuItems()).toEqual([{ title: "File", items: [] }]);
    invoke.mockResolvedValueOnce("not a list");
    expect(await frameMenuItems()).toEqual([]);
  });

  it("draws a preview without the shell, for the visual references", async () => {
    (window as unknown as W).__MAKTABA_FRAME_PREVIEW__ = { controls: "gtk", layout: ":close" };
    expect(await claimFrame()).toEqual({
      controls: "gtk",
      layout: ":close",
      side: "end",
      maximized: false,
      fullscreen: false,
      snapLayouts: false,
    });
    await frameAction("close");
    expect(invoke).not.toHaveBeenCalled();
  });
});
