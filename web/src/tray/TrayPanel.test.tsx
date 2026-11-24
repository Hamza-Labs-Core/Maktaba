// Story 13.12: the tray's panel draws the tray menu's own entries and acts through them.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));
vi.mock("@tauri-apps/api/event", () => ({ listen: vi.fn(async () => () => {}) }));

import { TrayPanel, partsOf } from "./TrayPanel";
import type { TrayPanelEntry, TrayPanelState } from "../lib/trayPanel";

const item = (id: string, label: string, enabled = true): TrayPanelEntry => ({
  id,
  label,
  enabled,
  separator: false,
});
const sep: TrayPanelEntry = { id: "", label: "", enabled: false, separator: true };

// the tray's entries for a server that runs, with the language model downloading and an
// update ready (tray.rs entries_with_order)
const RUNNING: TrayPanelEntry[] = [
  item("status", "Maktaba is running", false),
  item("model-status", "Language model: downloading qwen (64%)", false),
  item("update", "Update ready: restart to install"),
  sep,
  item("show", "Open Maktaba"),
  item("server-pause", "Pause server"),
  item("open-logs", "Open logs"),
  item("open-components", "Open Components"),
  sep,
  item("media-play", "Play"),
  item("media-pause", "Pause playback"),
  sep,
  item("tray-settings", "Settings"),
  item("report-problem", "Report a problem…"),
  sep,
  item("quit", "Quit (server keeps running)"),
  item("stop-and-quit", "Stop server and quit"),
];

function shell(state: TrayPanelState) {
  (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {};
  invoke.mockImplementation(async (cmd: string) => (cmd === "tray_panel_state" ? state : null));
}

describe("partsOf", () => {
  it("splits the tray's entries into the status, its lines, the update, the actions and the quits", () => {
    const p = partsOf(RUNNING);
    expect(p.status?.label).toBe("Maktaba is running");
    expect(p.lines.map((l) => l.id)).toEqual(["model-status"]);
    expect(p.update?.id).toBe("update");
    expect(p.groups.map((g) => g.map((e) => e.id))).toEqual([
      ["show", "server-pause", "open-logs", "open-components"],
      ["media-play", "media-pause"],
      ["tray-settings", "report-problem"],
    ]);
    expect(p.quits.map((q) => q.id)).toEqual(["quit", "stop-and-quit"]);
  });

  it("keeps a release the home ordered as a line, not a button", () => {
    const p = partsOf([
      item("status", "Maktaba is running", false),
      item("update", "Maktaba will update to 0.3.113 and restart by 03:00", false),
      sep,
      item("show", "Open Maktaba"),
      sep,
      item("quit", "Quit"),
    ]);
    expect(p.update).toBeUndefined();
    expect(p.lines.map((l) => l.label)).toEqual([
      "Maktaba will update to 0.3.113 and restart by 03:00",
    ]);
  });
});

describe("TrayPanel", () => {
  beforeEach(() => invoke.mockReset());
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
    document.documentElement.dir = "";
  });

  it("draws the status, the update and every action, and chooses through the tray", async () => {
    shell({ lang: "en", tone: "ok", entries: RUNNING });
    render(<TrayPanel />);
    expect(await screen.findByRole("status")).toHaveTextContent("Maktaba is running");
    // the first action takes the focus: the update, ready to install
    expect(screen.getByRole("button", { name: /Update ready/ })).toHaveFocus();
    await userEvent.click(screen.getByRole("button", { name: "Open logs" }));
    expect(invoke).toHaveBeenCalledWith("tray_panel_choose", { id: "open-logs" });
    await userEvent.click(screen.getByRole("button", { name: "Stop server and quit" }));
    expect(invoke).toHaveBeenCalledWith("tray_panel_choose", { id: "stop-and-quit" });
  });

  it("moves with the arrow keys and hides on Escape", async () => {
    shell({ lang: "en", tone: "ok", entries: RUNNING });
    render(<TrayPanel />);
    const update = await screen.findByRole("button", { name: /Update ready/ });
    expect(update).toHaveFocus();
    await userEvent.keyboard("{ArrowDown}");
    expect(screen.getByRole("button", { name: "Open Maktaba" })).toHaveFocus();
    await userEvent.keyboard("{ArrowUp}{ArrowUp}");
    expect(screen.getByRole("button", { name: "Stop server and quit" })).toHaveFocus();
    await userEvent.keyboard("{Escape}");
    expect(invoke).toHaveBeenCalledWith("tray_panel_hide", undefined);
  });

  it("speaks the tray's language, right to left in Arabic", async () => {
    shell({
      lang: "ar",
      tone: "error",
      entries: [
        item("status", "في مكتبة مشكلة", false),
        sep,
        item("show", "فتح مكتبة"),
        sep,
        item("quit", "خروج"),
      ],
    });
    render(<TrayPanel />);
    await screen.findByRole("status");
    expect(document.documentElement.dir).toBe("rtl");
    expect(document.documentElement.lang).toBe("ar");
    const group = screen.getByRole("group");
    expect(within(group).getByRole("button", { name: "فتح مكتبة" })).toBeInTheDocument();
  });

  it("follows the tray when its entries change", async () => {
    const { listen } = await import("@tauri-apps/api/event");
    let push: (s: TrayPanelState) => void = () => {};
    vi.mocked(listen).mockImplementationOnce(async (_name, cb) => {
      push = (s) => (cb as (e: { payload: TrayPanelState }) => void)({ payload: s });
      return () => {};
    });
    shell({
      lang: "en",
      tone: "busy",
      entries: [item("status", "Maktaba is starting…", false), sep, item("quit", "Quit")],
    });
    render(<TrayPanel />);
    expect(await screen.findByRole("status")).toHaveTextContent("starting");
    await act(async () => push({ lang: "en", tone: "ok", entries: RUNNING }));
    expect(screen.getByRole("status")).toHaveTextContent("Maktaba is running");
  });
});
