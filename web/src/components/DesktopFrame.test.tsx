// Story 13.10: Maktaba's own window frame (components/DesktopFrame.tsx).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { invoke } = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke }));
vi.mock("@tauri-apps/api/event", () => ({ listen: vi.fn(async () => () => {}) }));

import { DesktopFrame, FrameStart, WindowControls, useFrameHost } from "./DesktopFrame";
import { I18nProvider } from "../lib/i18n";
import type { FrameInfo, FrameMenuGroup } from "../lib/frame";

type W = Record<string, unknown>;

const MENU: FrameMenuGroup[] = [
  {
    title: "Maktaba",
    items: [
      { id: "preferences", label: "Settings…", enabled: true, separator: false },
      { id: "", label: "", enabled: false, separator: true },
      { id: "switch-server", label: "Switch Server…", enabled: false, separator: false },
    ],
  },
  {
    title: "View",
    items: [
      {
        id: "big-screen-start",
        label: "Start in Big Screen",
        enabled: true,
        checked: true,
        separator: false,
      },
    ],
  },
  {
    title: "",
    items: [{ id: "frame-quit", label: "Quit Maktaba", enabled: true, separator: false }],
  },
];

function shell(info: Partial<FrameInfo>) {
  (window as unknown as W).__TAURI_INTERNALS__ = {};
  invoke.mockImplementation(async (cmd: string) => {
    if (cmd === "frame_claim")
      return {
        controls: "windows",
        layout: "",
        side: "end",
        maximized: false,
        snapLayouts: false,
        ...info,
      };
    if (cmd === "frame_menu_items") return MENU;
    return null;
  });
}

function Header() {
  const frame = useFrameHost();
  return (
    <header data-testid="header" data-tauri-drag-region={frame ? "" : undefined}>
      {frame && <FrameStart />}
      <span>Maktaba</span>
      {frame && <WindowControls />}
    </header>
  );
}

function renderFrame(children: React.ReactNode = <main>page</main>) {
  return render(
    <I18nProvider>
      <DesktopFrame />
      {children}
    </I18nProvider>
  );
}

describe("DesktopFrame", () => {
  beforeEach(() => invoke.mockReset());
  afterEach(() => {
    delete (window as unknown as W).__TAURI_INTERNALS__;
    delete document.documentElement.dataset.frame;
    vi.useRealTimers();
  });

  it("draws nothing outside the desktop app: the window keeps the system's frame", async () => {
    renderFrame();
    await act(async () => {});
    expect(screen.queryByTestId("frame-bar")).toBeNull();
    expect(document.documentElement.dataset.frame).toBeUndefined();
  });

  it("gives a page without the app's header a slim title bar with Windows' buttons", async () => {
    shell({ controls: "windows" });
    renderFrame();
    const bar = await screen.findByTestId("frame-bar");
    expect(bar).toHaveAttribute("data-tauri-drag-region", "");
    expect(within(bar).getByRole("button", { name: "Minimize" })).toBeInTheDocument();
    expect(within(bar).getByRole("button", { name: "Maximize" })).toBeInTheDocument();
    expect(within(bar).getByRole("button", { name: "Close" })).toBeInTheDocument();
    expect(document.documentElement.dataset.frame).toBe("windows");
  });

  it("puts the frame in the app's header instead, so there is one bar", async () => {
    shell({ controls: "windows", maximized: true });
    renderFrame(<Header />);
    const header = await screen.findByTestId("header");
    await waitFor(() => expect(header).toHaveAttribute("data-tauri-drag-region", ""));
    expect(screen.queryByTestId("frame-bar")).toBeNull();
    expect(within(header).getByRole("button", { name: "Restore down" })).toBeInTheDocument();
    expect(within(header).getByRole("button", { name: "Menu" })).toBeInTheDocument();
  });

  it("sends the caption buttons to the shell", async () => {
    shell({ controls: "windows" });
    renderFrame();
    await userEvent.click(await screen.findByRole("button", { name: "Close" }));
    expect(invoke).toHaveBeenCalledWith("frame_action", { action: "close" });
    await userEvent.click(screen.getByRole("button", { name: "Minimize" }));
    expect(invoke).toHaveBeenCalledWith("frame_action", { action: "minimize" });
  });

  it("asks for Snap Layouts when the pointer rests on maximize, on Windows 11", async () => {
    shell({ controls: "windows", snapLayouts: true });
    renderFrame();
    const max = await screen.findByTestId("frame-maximize");
    vi.useFakeTimers();
    act(() => {
      max.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
    });
    await act(async () => {
      vi.advanceTimersByTime(700);
    });
    expect(invoke).toHaveBeenCalledWith("frame_action", { action: "snap" });
  });

  it("does not ask when the pointer only passes over", async () => {
    shell({ controls: "windows", snapLayouts: true });
    renderFrame();
    const max = await screen.findByTestId("frame-maximize");
    vi.useFakeTimers();
    act(() => {
      max.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      max.dispatchEvent(new MouseEvent("mouseout", { bubbles: true }));
    });
    await act(async () => {
      vi.advanceTimersByTime(700);
    });
    expect(invoke).not.toHaveBeenCalledWith("frame_action", { action: "snap" });
  });

  it("follows GTK's button layout on Linux", async () => {
    shell({ controls: "gtk", layout: "close:" });
    renderFrame();
    const bar = await screen.findByTestId("frame-bar");
    const buttons = within(bar).getAllByRole("button");
    // the close button before the menu, at the start
    expect(buttons[0]).toHaveAccessibleName("Close");
    expect(within(bar).queryByRole("button", { name: "Minimize" })).toBeNull();
  });

  it("leaves the traffic lights' room on the Mac and draws no buttons or menu", async () => {
    shell({ controls: "mac", side: "left" });
    const { container } = renderFrame();
    const bar = await screen.findByTestId("frame-bar");
    expect(container.querySelector(".mkt-frame__traffic")).not.toBeNull();
    expect(within(bar).queryAllByRole("button")).toHaveLength(0);
    expect(document.documentElement.dataset.frameSide).toBe("left");
  });

  it("lists the menu bar's items in the title bar's menu, and chooses one", async () => {
    shell({ controls: "windows" });
    renderFrame();
    await userEvent.click(await screen.findByRole("button", { name: "Menu" }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByRole("menuitem", { name: /Settings/ })).toHaveFocus();
    expect(within(menu).getByRole("menuitem", { name: /Switch Server/ })).toBeDisabled();
    expect(
      within(menu).getByRole("menuitemcheckbox", { name: /Start in Big Screen/ })
    ).toHaveAttribute("aria-checked", "true");
    // the arrow keys skip the disabled item
    await userEvent.keyboard("{ArrowDown}");
    expect(within(menu).getByRole("menuitemcheckbox")).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    expect(invoke).toHaveBeenCalledWith("frame_menu", { id: "big-screen-start" });
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("names each group of the menu by its menu's title, for a screen reader", async () => {
    shell({ controls: "windows" });
    renderFrame();
    await userEvent.click(await screen.findByRole("button", { name: "Menu" }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByRole("group", { name: "View" })).toContainElement(
      within(menu).getByRole("menuitemcheckbox", { name: /Start in Big Screen/ })
    );
  });

  it("never drags the window from inside the menu, and closes it on a press outside", async () => {
    shell({ controls: "windows" });
    renderFrame();
    await userEvent.click(await screen.findByRole("button", { name: "Menu" }));
    const menu = await screen.findByRole("menu");
    // only the bar itself drags ("": that element only, Tauri's drag.js): nothing around the
    // menu drags its subtree, and nothing in it is a drag region
    expect(menu.closest('[data-tauri-drag-region="deep"]')).toBeNull();
    expect(menu.querySelector("[data-tauri-drag-region]")).toBeNull();
    // a press outside closes it even when Tauri's drag region stops the document's mousedown
    act(() => {
      // jsdom has no PointerEvent: a plain event of the type is what the listener reads
      document.body.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    });
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("draws no bar when the window is already full screen (Start in Big Screen)", async () => {
    shell({ controls: "windows", fullscreen: true });
    renderFrame();
    await act(async () => {});
    await act(async () => {});
    expect(screen.queryByTestId("frame-bar")).toBeNull();
    expect(document.documentElement.dataset.frame).toBeUndefined();
  });

  it("closes the menu with Escape, focus back on its button", async () => {
    shell({ controls: "windows" });
    renderFrame();
    const trigger = await screen.findByRole("button", { name: "Menu" });
    await userEvent.click(trigger);
    await screen.findByRole("menu");
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(trigger).toHaveFocus();
  });
});
