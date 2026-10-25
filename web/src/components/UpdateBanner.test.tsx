// Story 28.5 — the mobile app's update banner: shown inside the app when a newer release is
// offered, with this phone's action and the notes; Dismiss hides it for that version; never
// in a browser.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../lib/i18n";
import { isDismissed, PLAY_STORE_URL, type AppRelease } from "../lib/appUpdate";
import { UpdateBanner } from "./UpdateBanner";
import { AppUpdateSettings } from "../pages/Settings/AppUpdateSettings";

const rel: AppRelease = {
  version: "1.4.2",
  releaseUrl: "https://github.com/Hamza-Labs-Core/Maktaba/releases/tag/v1.4.2",
  apkUrl: "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.4.2/maktaba-1.4.2.apk",
  source: "server",
};

const wrap = (ui: React.ReactNode) => render(<I18nProvider>{ui}</I18nProvider>);

describe("UpdateBanner", () => {
  beforeEach(() => localStorage.clear());

  it("says a new version is available, with the .apk and the notes (Android sideload)", async () => {
    const offer = vi.fn(() => Promise.resolve(rel));
    wrap(<UpdateBanner current="1.4.1" platform="android" offer={offer} />);
    expect(await screen.findByText("New version available — v1.4.2")).toBeInTheDocument();
    expect(offer).toHaveBeenCalledWith("1.4.1");
    expect(screen.getByRole("link", { name: "Get update" })).toHaveAttribute("href", rel.apkUrl);
    expect(screen.getByRole("link", { name: "Release notes" })).toHaveAttribute(
      "href",
      rel.releaseUrl
    );
  });

  it("hides on Dismiss, for that version", async () => {
    wrap(<UpdateBanner current="1.4.1" platform="ios" offer={() => Promise.resolve(rel)} />);
    await userEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText(/New version available/)).not.toBeInTheDocument();
    expect(isDismissed("1.4.2")).toBe(true);
    expect(isDismissed("1.4.3")).toBe(false);
  });

  it("shows nothing when nothing is offered, and never checks in a browser", async () => {
    const offer = vi.fn(() => Promise.resolve(null));
    const { container } = wrap(<UpdateBanner current="1.4.2" platform="android" offer={offer} />);
    await vi.waitFor(() => expect(offer).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();

    const web = vi.fn(() => Promise.resolve(rel));
    const other = wrap(<UpdateBanner current="1.4.1" platform="web" offer={web} />);
    expect(web).not.toHaveBeenCalled();
    expect(other.container).toBeEmptyDOMElement();
  });

  it("is in Arabic under the Arabic locale", async () => {
    localStorage.setItem("mkt:locale", "ar");
    wrap(<UpdateBanner current="1.4.1" platform="android" offer={() => Promise.resolve(rel)} />);
    expect(await screen.findByText("يتوفر إصدار جديد — v1.4.2")).toBeInTheDocument();
  });

  it("keeps the store build's action on the Play Store", async () => {
    vi.stubEnv("VITE_DISTRIBUTION", "store");
    try {
      wrap(<UpdateBanner current="1.4.1" platform="android" offer={() => Promise.resolve(rel)} />);
      expect(await screen.findByRole("link", { name: "Get update" })).toHaveAttribute(
        "href",
        PLAY_STORE_URL
      );
    } finally {
      vi.unstubAllEnvs();
    }
  });
});

describe("AppUpdateSettings", () => {
  beforeEach(() => localStorage.clear());

  it("sets how often the app looks for a new version, inside the app only", async () => {
    wrap(<AppUpdateSettings platform="android" />);
    expect(screen.getByRole("radio", { name: "Daily" })).toBeChecked();
    await userEvent.click(screen.getByRole("radio", { name: "Never" }));
    expect(localStorage.getItem("mkt:update:frequency")).toBe("off");

    const web = wrap(<AppUpdateSettings platform="web" />);
    expect(web.container).toBeEmptyDOMElement();
  });
});
