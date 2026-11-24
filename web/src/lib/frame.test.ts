// Story 13.10: the page's side of the desktop app's own frame (lib/frame.ts).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));

// story 13.13: Tauri's events as they are delivered: one sent to a window (emit_to) reaches
// that window's listeners, and every global listener whatever window it was sent to
const bus = vi.hoisted(() => {
  type Cb = (e: { payload: unknown }) => void;
  const all: { label: string | null; event: string; cb: Cb }[] = [];
  return {
    current: "main",
    all,
    add(label: string | null, event: string, cb: Cb) {
      all.push({ label, event, cb });
      return () => {};
    },
    emitTo(label: string, event: string, payload: unknown) {
      for (const l of all)
        if (l.event === event && (l.label === null || l.label === label)) l.cb({ payload });
    },
  };
});
vi.mock("@tauri-apps/api/event", () => ({
  listen: async (event: string, cb: (e: { payload: unknown }) => void) => bus.add(null, event, cb),
}));
vi.mock("@tauri-apps/api/webviewWindow", () => ({
  getCurrentWebviewWindow: () => {
    const label = bus.current;
    return {
      label,
      listen: async (event: string, cb: (e: { payload: unknown }) => void) =>
        bus.add(label, event, cb),
    };
  },
}));

import {
  chooseFrameMenu,
  claimFrame,
  frameAction,
  frameMenuItems,
  gtkButtons,
  onFrameState,
  type FrameState,
} from "./frame";

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

describe("the window's state (story 13.13)", () => {
  afterEach(() => {
    delete (window as unknown as W).__TAURI_INTERNALS__;
    bus.all.length = 0;
    bus.current = "main";
  });

  it("each window hears only its own: the main window's full screen leaves the setup's bar", async () => {
    (window as unknown as W).__TAURI_INTERNALS__ = {};
    const main: FrameState[] = [];
    const setup: FrameState[] = [];
    bus.current = "main";
    await onFrameState((s) => main.push(s));
    bus.current = "setup";
    await onFrameState((s) => setup.push(s));
    bus.emitTo("main", "frame-state", { maximized: false, fullscreen: true });
    expect(main).toEqual([{ maximized: false, fullscreen: true }]);
    expect(setup).toEqual([]);
    bus.emitTo("setup", "frame-state", { maximized: true });
    expect(setup).toEqual([{ maximized: true }]);
    expect(main).toHaveLength(1);
    // no listener hears every window's
    expect(bus.all.every((l) => l.label !== null)).toBe(true);
  });
});
