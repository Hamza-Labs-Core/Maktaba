// Story 13.13: the desktop app marks its setup and status windows before their page loads.
import { afterEach, describe, expect, it } from "vitest";
import { ownWindow } from "./ownWindow";

type W = { __MAKTABA_PANEL__?: unknown };

describe("ownWindow", () => {
  afterEach(() => {
    delete (window as unknown as W).__MAKTABA_PANEL__;
  });

  it("is none in the main window and the browser", () => {
    expect(ownWindow()).toBeNull();
  });

  it("names the setup and status windows, and nothing else", () => {
    const w = window as unknown as W;
    w.__MAKTABA_PANEL__ = "setup";
    expect(ownWindow()).toBe("setup");
    w.__MAKTABA_PANEL__ = "status";
    expect(ownWindow()).toBe("status");
    // the tray's panel and the shell's dialog are other stories' pages
    w.__MAKTABA_PANEL__ = "tray";
    expect(ownWindow()).toBeNull();
    w.__MAKTABA_PANEL__ = { status: true };
    expect(ownWindow()).toBeNull();
  });
});
