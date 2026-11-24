// Story 13.11: Maktaba's own dialog for the shell's errors (dialog/ShellDialog.tsx).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));
const { listen } = vi.hoisted(() => ({
  listen: vi.fn(async (_name: string, _cb: unknown) => () => {}),
}));
vi.mock("@tauri-apps/api/event", () => ({ listen }));

import { ShellDialog } from "./ShellDialog";
import type { DialogContent } from "../lib/shellDialog";

type W = Record<string, unknown>;

const PORT: DialogContent = {
  lang: "en",
  brand: "Maktaba",
  title: "The Maktaba server could not start.",
  errors: ["maktaba-api: listen tcp 0.0.0.0:8080: bind: address already in use"],
  logs: true,
  close: "Close",
  seeLogs: "See the logs",
};

function open(content: DialogContent) {
  const w = window as unknown as W;
  w.__TAURI_INTERNALS__ = {};
  w.__MAKTABA_PANEL__ = "dialog";
  w.__MAKTABA_DIALOG__ = content;
  return render(<ShellDialog />);
}

describe("ShellDialog", () => {
  beforeEach(() => {
    invoke.mockReset();
    invoke.mockResolvedValue(null);
  });
  afterEach(() => {
    for (const k of ["__TAURI_INTERNALS__", "__MAKTABA_PANEL__", "__MAKTABA_DIALOG__"])
      delete (window as unknown as W)[k];
    document.documentElement.dir = "";
  });

  it("says what happened, and the programs' own errors as they are", () => {
    open(PORT);
    const dialog = screen.getByRole("alertdialog", { name: PORT.title });
    expect(dialog).toHaveAccessibleDescription(PORT.errors[0]!);
    // the errors stay left to right, in an Arabic dialog too
    expect(screen.getByText(PORT.errors[0]!)).toHaveAttribute("dir", "ltr");
  });

  it("has the focus on Close, so Enter closes, and Escape closes too", async () => {
    open(PORT);
    // the title bar's ✕ and the foot's Close share the name: the foot's has the focus
    const [, close] = screen.getAllByRole("button", { name: "Close" });
    expect(close).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    expect(invoke).toHaveBeenCalledWith("shell_dialog_choose", { action: "close" });
    invoke.mockClear();
    await userEvent.keyboard("{Escape}");
    expect(invoke).toHaveBeenCalledWith("shell_dialog_choose", { action: "close" });
    expect(close).toBeInTheDocument();
  });

  it("offers the logs only when this machine has them", async () => {
    open(PORT);
    await userEvent.click(screen.getByRole("button", { name: "See the logs" }));
    expect(invoke).toHaveBeenCalledWith("shell_dialog_choose", { action: "logs" });
  });

  it("offers no logs in an app that runs no server here, and speaks Arabic right to left", () => {
    open({
      ...PORT,
      lang: "ar",
      brand: "مكتبة",
      title: "تعذّر فتح http://home.example.",
      errors: [],
      logs: false,
      close: "إغلاق",
      seeLogs: "عرض السجلات",
    });
    expect(screen.queryByRole("button", { name: "عرض السجلات" })).toBeNull();
    expect(document.documentElement.dir).toBe("rtl");
    expect(screen.getByRole("alertdialog")).not.toHaveAccessibleDescription(/./);
  });

  it("keeps an address in a sentence as its own left-to-right run", () => {
    open({ ...PORT, lang: "ar", title: "يتعذّر فتح http://192.168.1.20:8080.", errors: [] });
    const run = screen.getByText("http://192.168.1.20:8080");
    expect(run.tagName).toBe("BDI");
    expect(run).toHaveAttribute("dir", "ltr");
    // the full stop stays the sentence's, outside the run
    expect(screen.getByRole("heading")).toHaveTextContent("يتعذّر فتح http://192.168.1.20:8080.");
  });

  it("takes a second error's words while it is open", async () => {
    let push: (c: DialogContent) => void = () => {};
    listen.mockImplementationOnce(async (_name: string, cb: unknown) => {
      push = (c) => (cb as (e: { payload: DialogContent }) => void)({ payload: c });
      return () => {};
    });
    open(PORT);
    await act(async () => {});
    await act(async () =>
      push({ ...PORT, title: "The update could not be installed.", errors: [] })
    );
    expect(
      screen.getByRole("alertdialog", { name: "The update could not be installed." })
    ).toBeInTheDocument();
  });
});
