// Story 13.11: the shell dialog's bridge (lib/shellDialog.ts).
import { afterEach, describe, expect, it, vi } from "vitest";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));

import { chooseDialog, dialogContent, isShellDialog } from "./shellDialog";

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

  it("is the dialog only when the shell marked the page, with words it can read", () => {
    expect(isShellDialog()).toBe(false);
    expect(dialogContent()).toBeNull();
    const w = window as unknown as W;
    w.__MAKTABA_PANEL__ = "dialog";
    w.__MAKTABA_DIALOG__ = { title: 3 };
    expect(isShellDialog()).toBe(true);
    expect(dialogContent()).toBeNull();
    w.__MAKTABA_DIALOG__ = { title: "x", errors: [] };
    expect(dialogContent()).toEqual({ title: "x", errors: [] });
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
    w.__MAKTABA_DIALOG_PREVIEW__ = true;
    await chooseDialog("close");
    expect(invoke).not.toHaveBeenCalled();
  });
});
