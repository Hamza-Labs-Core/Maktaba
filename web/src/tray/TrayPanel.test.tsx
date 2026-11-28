// Story 13.12: the tray's panel draws the tray menu's own entries and acts through them.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { invoke, listen } = vi.hoisted(() => ({ invoke: vi.fn(), listen: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));
// its own window's events; the global `listen` would hear every window's, and be heard by all
vi.mock("@tauri-apps/api/webviewWindow", () => ({ getCurrentWebviewWindow: () => ({ listen }) }));

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

/** The shell's events, by name. */
let heard: Record<string, (e: { payload: unknown }) => void> = {};
/** The tray's entries changed: the shell says so, and answers with `state` from now on. */
async function change(state: TrayPanelState) {
  shell(state);
  await act(async () => heard["tray-panel-state"]!({ payload: null }));
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

  it("draws every action of an app connected to another Maktaba (no status, an update first)", () => {
    const p = partsOf([
      item("update", "Update ready: restart to install"),
      sep,
      item("show", "Open Maktaba"),
      item("change-server", "Connect to another Maktaba…"),
      sep,
      item("quit", "Quit"),
    ]);
    expect(p.status).toBeUndefined();
    expect(p.update?.id).toBe("update");
    expect(p.groups.map((g) => g.map((e) => e.id))).toEqual([["show", "change-server"]]);
  });

  it("draws the recovery actions of a server that cannot start, and its line", () => {
    const p = partsOf([
      item("cant-start", "Maktaba can't start: it stopped 3 times in a row", false),
      item("start-retry", "Try again"),
      item("start-safe", "Start in safe mode"),
      item("start-rollback", "Go back to version 0.3.111"),
      sep,
      item("status", "Maktaba has a problem: the server does not answer", false),
      item("model-status", "Language model: ready", false),
      sep,
      item("show", "Open Maktaba"),
      item("open-recovery", "Open recovery page"),
      sep,
      item("quit", "Quit (server keeps running)"),
      item("stop-and-quit", "Stop server and quit"),
    ]);
    expect(p.status?.id).toBe("status");
    expect(p.lines.map((l) => l.id)).toEqual(["cant-start", "model-status"]);
    expect(p.groups.map((g) => g.map((e) => e.id))).toEqual([
      ["start-retry", "start-safe", "start-rollback"],
      ["show", "open-recovery"],
    ]);
  });

  it("draws a failed update's line and its way to the logs", () => {
    const p = partsOf([
      item("status", "Maktaba is running", false),
      item("update-failed", "Update to 0.3.113 failed: no space left", false),
      item("update-logs", "See the logs"),
      item("update-log-folder", "Open the log folder"),
      sep,
      item("show", "Open Maktaba"),
      sep,
      item("quit", "Quit"),
    ]);
    expect(p.lines.map((l) => l.id)).toEqual(["update-failed"]);
    expect(p.groups[0]!.map((e) => e.id)).toEqual(["update-logs", "update-log-folder"]);
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
  beforeEach(() => {
    invoke.mockReset();
    heard = {};
    listen.mockReset();
    listen.mockImplementation(async (name: string, cb: (e: { payload: unknown }) => void) => {
      heard[name] = cb;
      return () => {};
    });
  });
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

  it("keeps the keys after a click on the status line, and the focus while the words change", async () => {
    shell({ lang: "en", tone: "ok", entries: RUNNING });
    render(<TrayPanel />);
    const status = await screen.findByRole("status");
    await userEvent.click(status);
    await userEvent.keyboard("{ArrowDown}");
    expect(screen.getByRole("button", { name: /Update ready/ })).toHaveFocus();
    await userEvent.keyboard("{ArrowDown}{ArrowDown}");
    const logs = screen.getByRole("button", { name: "Pause server" });
    expect(logs).toHaveFocus();
    // the model's percent changes: the focus stays where the person put it
    await change({
      lang: "en",
      tone: "ok",
      entries: RUNNING.map((e) =>
        e.id === "model-status" ? { ...e, label: "Language model: downloading qwen (65%)" } : e
      ),
    });
    expect(screen.getByText(/65%/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pause server" })).toHaveFocus();
    await userEvent.click(status);
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
    expect(
      within(screen.getByRole("navigation")).getByRole("button", { name: "فتح مكتبة" })
    ).toBeInTheDocument();
  });

  it("follows the tray when its entries change", async () => {
    shell({
      lang: "en",
      tone: "busy",
      entries: [item("status", "Maktaba is starting…", false), sep, item("quit", "Quit")],
    });
    render(<TrayPanel />);
    expect(await screen.findByRole("status")).toHaveTextContent("starting");
    await change({ lang: "en", tone: "ok", entries: RUNNING });
    expect(screen.getByRole("status")).toHaveTextContent("Maktaba is running");
  });

  it("takes the entries from the shell, never from an event another page could send", async () => {
    shell({ lang: "en", tone: "ok", entries: RUNNING });
    render(<TrayPanel />);
    await screen.findByRole("status");
    await act(async () =>
      heard["tray-panel-state"]!({
        payload: { lang: "en", tone: "error", entries: [item("status", "Forged", false)] },
      })
    );
    expect(screen.getByRole("status")).toHaveTextContent("Maktaba is running");
  });
});
