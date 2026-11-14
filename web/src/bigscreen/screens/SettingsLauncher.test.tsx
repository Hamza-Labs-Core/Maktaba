// Epic 58 story 58.09 — big screen Settings in the Samsung or LG launcher: Switch home opens
// the launcher's home picker, and the connection says whether it is encrypted, in English
// and Arabic. Outside the launcher neither shows.
import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "../../lib/i18n";
import { LauncherContext, type LauncherMode } from "../../tv/launcher";
import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { BigSettings } from "./Settings";
import { resetBridgeForTest, setLauncherHelloForTest, type LauncherHello } from "../input/bridge";

const E = en as Record<string, string>;
const A = ar as Record<string, string>;

function renderSettings(mode: LauncherMode | null) {
  return render(
    <I18nProvider>
      <LauncherContext.Provider value={mode}>
        <BigSettings onExit={null} />
      </LauncherContext.Provider>
    </I18nProvider>
  );
}

const mode = (plain: boolean): LauncherMode => ({ bundled: false, base: null, plain, lang: null });

afterEach(() => {
  resetBridgeForTest();
  localStorage.clear();
  Object.defineProperty(window, "parent", { value: window, configurable: true });
  vi.restoreAllMocks();
});

describe("Settings in the launcher", () => {
  it("Switch home asks the launcher for its home picker", () => {
    localStorage.setItem("mkt:locale", "en");
    const postMessage = vi.fn();
    Object.defineProperty(window, "parent", { value: { postMessage }, configurable: true });
    setLauncherHelloForTest({ messages: ["openHomePicker"] } as unknown as LauncherHello);
    renderSettings(mode(false));
    expect(screen.getByText(E["tv.account.encrypted"]!)).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("switch-home"));
    expect(postMessage).toHaveBeenCalledWith({ type: "maktaba:openHomePicker" }, "*");
  });

  it("over plain HTTP says so, in Arabic", () => {
    localStorage.setItem("mkt:locale", "ar");
    renderSettings(mode(true));
    expect(screen.getByText(A["tv.plainHttp"]!)).toBeInTheDocument();
    expect(screen.getByTestId("switch-home")).toHaveTextContent(A["tv.account.switchHome"]!);
  });

  it("not in the launcher: no Switch home", () => {
    renderSettings(null);
    expect(screen.queryByTestId("switch-home")).toBeNull();
  });
});
