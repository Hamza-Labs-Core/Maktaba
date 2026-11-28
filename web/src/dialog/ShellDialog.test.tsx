// Story 13.11: Maktaba's own dialog for the shell's errors (dialog/ShellDialog.tsx).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));
const { listen } = vi.hoisted(() => ({ listen: vi.fn() }));
// its own window's events; the global `listen` would hear every window's, and be heard by all
vi.mock("@tauri-apps/api/webviewWindow", () => ({ getCurrentWebviewWindow: () => ({ listen }) }));

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
  revision: 1,
};

/** The shell's events, by name. */
let heard: Record<string, (e: { payload: unknown }) => void> = {};

async function open(content: DialogContent) {
  const w = window as unknown as W;
  w.__TAURI_INTERNALS__ = {};
  w.__MAKTABA_PANEL__ = "dialog";
  invoke.mockImplementation(async (cmd: string) =>
    cmd === "shell_dialog_content" ? content : null
  );
  render(<ShellDialog />);
  await screen.findByRole("alertdialog");
}

describe("ShellDialog", () => {
  beforeEach(() => {
    invoke.mockReset();
    invoke.mockResolvedValue(null);
    heard = {};
    listen.mockReset();
    listen.mockImplementation(async (name: string, cb: (e: { payload: unknown }) => void) => {
      heard[name] = cb;
      return () => {};
    });
  });
  afterEach(() => {
    for (const k of ["__TAURI_INTERNALS__", "__MAKTABA_PANEL__"])
      delete (window as unknown as W)[k];
    document.documentElement.dir = "";
  });

  it("says what happened, and the programs' own errors as they are", async () => {
    await open(PORT);
    // asking for its words is what shows the dialog
    expect(invoke).toHaveBeenCalledWith("shell_dialog_content");
    const dialog = screen.getByRole("alertdialog", { name: PORT.title });
    expect(dialog).toHaveAccessibleDescription(PORT.errors[0]!);
    // the errors stay left to right, in an Arabic dialog too
    expect(screen.getByText(PORT.errors[0]!)).toHaveAttribute("dir", "ltr");
  });

  it("has the focus on Close, so Enter closes, and Escape closes too", async () => {
    await open(PORT);
    // the title bar's ✕ shares the name but is no tab stop: the foot's Close has the focus
    const [x, close] = screen.getAllByRole("button", { name: "Close" });
    expect(x).toHaveAttribute("tabindex", "-1");
    expect(close).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    expect(invoke).toHaveBeenCalledWith("shell_dialog_choose", { action: "close" });
    invoke.mockClear();
    await userEvent.keyboard("{Escape}");
    expect(invoke).toHaveBeenCalledWith("shell_dialog_choose", { action: "close" });
  });

  it("offers the logs only when this machine has them", async () => {
    await open(PORT);
    await userEvent.click(screen.getByRole("button", { name: "See the logs" }));
    expect(invoke).toHaveBeenCalledWith("shell_dialog_choose", { action: "logs" });
  });

  it("offers no logs in an app that runs no server here, and speaks Arabic right to left", async () => {
    await open({
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

  it("keeps an address in a sentence as its own left-to-right run", async () => {
    await open({ ...PORT, lang: "ar", title: "يتعذّر فتح http://192.168.1.20:8080.", errors: [] });
    const run = screen.getByText("http://192.168.1.20:8080");
    expect(run.tagName).toBe("BDI");
    expect(run).toHaveAttribute("dir", "ltr");
    // the full stop stays the sentence's, outside the run
    expect(screen.getByRole("heading")).toHaveTextContent("يتعذّر فتح http://192.168.1.20:8080.");
  });

  it("takes a second error's words while it is open, from the shell and never from the event", async () => {
    await open(PORT);
    const second = {
      ...PORT,
      title: "The update could not be installed.",
      errors: [],
      revision: 2,
    };
    invoke.mockImplementation(async () => second);
    await act(async () =>
      heard["shell-dialog"]!({ payload: { ...PORT, title: "Forged", revision: 9 } })
    );
    expect(screen.getByRole("alertdialog", { name: second.title })).toBeInTheDocument();
  });

  it("keeps the newest error's words when an older answer comes late", async () => {
    const w = window as unknown as W;
    w.__TAURI_INTERNALS__ = {};
    w.__MAKTABA_PANEL__ = "dialog";
    const second = { ...PORT, title: "The update could not be installed.", revision: 2 };
    let first: (c: DialogContent) => void = () => {};
    invoke.mockImplementationOnce(() => new Promise((r) => (first = r)));
    invoke.mockImplementation(async () => second);
    render(<ShellDialog />);
    await act(async () => {});
    await act(async () => heard["shell-dialog"]!({ payload: null }));
    expect(screen.getByRole("alertdialog", { name: second.title })).toBeInTheDocument();
    await act(async () => first(PORT));
    expect(screen.getByRole("alertdialog", { name: second.title })).toBeInTheDocument();
  });
});
