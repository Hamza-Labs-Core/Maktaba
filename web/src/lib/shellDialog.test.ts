// Story 13.11: the shell dialog's bridge (lib/shellDialog.ts).
import { afterEach, describe, expect, it, vi } from "vitest";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));

import { chooseDialog, isShellDialog, loadDialogContent } from "./shellDialog";

type W = Record<string, unknown>;

describe("the shell dialog's bridge", () => {
  afterEach(() => {
    invoke.mockReset();
    for (const k of [
      "__TAURI_INTERNALS__",
      "__MAKTABA_PANEL__",
      "__MAKTABA_DIALOG__",
      "__MAKTABA_DIALOG_PREVIEW__",
    ])
      delete (window as unknown as W)[k];
  });

  it("is the dialog only when the shell marked the page", () => {
    expect(isShellDialog()).toBe(false);
    (window as unknown as W).__MAKTABA_PANEL__ = "dialog";
    expect(isShellDialog()).toBe(true);
  });

  it("asks the shell for the newest words, and refuses words it cannot draw", async () => {
    expect(await loadDialogContent()).toBeNull();
    expect(invoke).not.toHaveBeenCalled();
    (window as unknown as W).__TAURI_INTERNALS__ = {};
    const good = {
      lang: "en",
      brand: "Maktaba",
      title: "x",
      errors: [],
      logs: false,
      close: "Close",
      seeLogs: "See the logs",
      revision: 1,
    };
    invoke.mockResolvedValueOnce(good);
    expect(await loadDialogContent()).toEqual(good);
    expect(invoke).toHaveBeenCalledWith("shell_dialog_content");
    invoke.mockResolvedValueOnce({ title: "x", errors: [] });
    expect(await loadDialogContent()).toBeNull();
    invoke.mockResolvedValueOnce(null);
    expect(await loadDialogContent()).toBeNull();
  });

  it("sends a choice to the shell, and none from a preview or a browser", async () => {
    await chooseDialog("close");
    expect(invoke).not.toHaveBeenCalled();
    const w = window as unknown as W;
    w.__TAURI_INTERNALS__ = {};
    invoke.mockRejectedValueOnce(new Error("no logs to open here"));
    await expect(chooseDialog("logs")).resolves.toBeUndefined();
    expect(invoke).toHaveBeenCalledWith("shell_dialog_choose", { action: "logs" });
    invoke.mockClear();
    w.__MAKTABA_DIALOG_PREVIEW__ = { title: "preview" };
    await chooseDialog("close");
    expect(invoke).not.toHaveBeenCalled();
  });
});
