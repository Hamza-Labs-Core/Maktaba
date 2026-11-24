// Story 13.13: the status window's side of the shell (lib/statusWindow.ts).
import { afterEach, describe, expect, it, vi } from "vitest";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));
const { listen } = vi.hoisted(() => ({ listen: vi.fn() }));
vi.mock("@tauri-apps/api/event", () => ({ listen }));

import {
  canResume,
  onStatusWindow,
  statusTone,
  statusWindowAction,
  statusWindowState,
  type StatusWindowView,
} from "./statusWindow";

type W = Record<string, unknown>;

const VIEW: StatusWindowView = {
  status: { kind: "running" },
  host: "Haddad-PC",
  version: "0.3.112",
  parts: [{ id: "server", tone: "ok", running: 1 }],
};

describe("statusWindow", () => {
  afterEach(() => {
    delete (window as unknown as W).__TAURI_INTERNALS__;
    delete (window as unknown as W).__MAKTABA_STATUS_PREVIEW__;
    invoke.mockReset();
    listen.mockReset();
  });

  it("is nothing outside the desktop app", async () => {
    expect(await statusWindowState()).toBeNull();
    await statusWindowAction("pause");
    expect(invoke).not.toHaveBeenCalled();
    const stop = await onStatusWindow(() => {});
    expect(listen).not.toHaveBeenCalled();
    stop();
  });

  it("asks the shell for its view, sends its buttons, and hears each new view", async () => {
    (window as unknown as W).__TAURI_INTERNALS__ = {};
    invoke.mockImplementation(async (cmd: string) =>
      cmd === "status_window_state" ? VIEW : undefined
    );
    expect(await statusWindowState()).toEqual(VIEW);
    await statusWindowAction("resume");
    expect(invoke).toHaveBeenCalledWith("status_window_action", { action: "resume" });

    let heard: (e: { payload: unknown }) => void = () => {};
    listen.mockImplementation(async (_: string, cb: typeof heard) => {
      heard = cb;
      return () => {};
    });
    const seen: StatusWindowView[] = [];
    await onStatusWindow((v) => seen.push(v));
    expect(listen).toHaveBeenCalledWith("status-window", expect.any(Function));
    heard({ payload: { nonsense: true } });
    heard({ payload: VIEW });
    expect(seen).toEqual([VIEW]);
  });

  it("a view that is not one is none", async () => {
    (window as unknown as W).__TAURI_INTERNALS__ = {};
    invoke.mockResolvedValue({ status: "running" });
    expect(await statusWindowState()).toBeNull();
  });

  it("the visual references stand in for the shell, and send nothing", async () => {
    (window as unknown as W).__MAKTABA_STATUS_PREVIEW__ = VIEW;
    expect(await statusWindowState()).toBe(VIEW);
    await statusWindowAction("open");
    expect(invoke).not.toHaveBeenCalled();
  });

  it("resumes a paused or stopped server, pauses any other", () => {
    expect(canResume({ kind: "paused" })).toBe(true);
    expect(canResume({ kind: "stopped" })).toBe(true);
    expect(canResume({ kind: "running" })).toBe(false);
    expect(canResume({ kind: "problem", roles: ["api"] })).toBe(false);
  });

  it("colours the state as the tray's badge does", () => {
    expect(statusTone({ kind: "running" })).toBe("ok");
    expect(statusTone({ kind: "starting" })).toBe("busy");
    expect(statusTone({ kind: "problem", roles: [] })).toBe("error");
    expect(statusTone({ kind: "not_answering", reason: "x" })).toBe("error");
    expect(statusTone({ kind: "paused" })).toBe("idle");
  });
});
