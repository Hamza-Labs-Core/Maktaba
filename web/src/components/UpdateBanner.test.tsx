// Story 28.5 — the mobile app's update banner: shown in the sideloaded Android app when a
// newer release is offered, with the release's .apk (else its page) and the notes; Dismiss
// hides it for that version; never on a store build, on iOS or in a browser.
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../lib/i18n";
import { isDismissed, type AppRelease } from "../lib/appUpdate";
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

  it("says a new version is available, with the .apk and the notes", async () => {
    const offer = vi.fn(() => Promise.resolve(rel));
    wrap(<UpdateBanner current="1.4.1" platform="android" dist="sideload" offer={offer} />);
    expect(await screen.findByText("New version available — v1.4.2")).toBeInTheDocument();
    expect(offer).toHaveBeenCalledWith("1.4.1");
    expect(screen.getByRole("link", { name: "Get update" })).toHaveAttribute("href", rel.apkUrl);
    expect(screen.getByRole("link", { name: "Release notes" })).toHaveAttribute(
      "href",
      rel.releaseUrl
    );
  });

  it("opens the release page when the release lists no .apk (one link, not two)", async () => {
    const page = { ...rel, apkUrl: undefined };
    wrap(
      <UpdateBanner
        current="1.4.1"
        platform="android"
        dist="sideload"
        offer={() => Promise.resolve(page)}
      />
    );
    expect(await screen.findByRole("link", { name: "Get update" })).toHaveAttribute(
      "href",
      rel.releaseUrl
    );
    expect(screen.queryByRole("link", { name: "Release notes" })).not.toBeInTheDocument();
  });

  it("hides on Dismiss, for that version", async () => {
    wrap(
      <UpdateBanner
        current="1.4.1"
        platform="android"
        dist="sideload"
        offer={() => Promise.resolve(rel)}
      />
    );
    await userEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText(/New version available/)).not.toBeInTheDocument();
    expect(isDismissed("1.4.2")).toBe(true);
    expect(isDismissed("1.4.3")).toBe(false);
  });

  it("shows nothing when nothing is offered", async () => {
    const offer = vi.fn(() => Promise.resolve(null));
    const { container } = wrap(
      <UpdateBanner current="1.4.2" platform="android" dist="sideload" offer={offer} />
    );
    await vi.waitFor(() => expect(offer).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("never checks on a store build, on iOS or in a browser (their stores update them)", () => {
    for (const [platform, dist] of [
      ["android", "store"],
      ["ios", undefined],
      ["web", "sideload"],
    ] as const) {
      const offer = vi.fn(() => Promise.resolve(rel));
      const { container, unmount } = wrap(
        <UpdateBanner current="1.4.1" platform={platform} dist={dist} offer={offer} />
      );
      expect(offer).not.toHaveBeenCalled();
      expect(container).toBeEmptyDOMElement();
      unmount();
    }
  });

  it("is in Arabic under the Arabic locale", async () => {
    localStorage.setItem("mkt:locale", "ar");
    wrap(
      <UpdateBanner
        current="1.4.1"
        platform="android"
        dist="sideload"
        offer={() => Promise.resolve(rel)}
      />
    );
    expect(await screen.findByText("يتوفر إصدار جديد — v1.4.2")).toBeInTheDocument();
  });

  it("takes the build from VITE_DISTRIBUTION: the Play build shows nothing", () => {
    vi.stubEnv("VITE_DISTRIBUTION", "store");
    try {
      const offer = vi.fn(() => Promise.resolve(rel));
      const { container } = wrap(<UpdateBanner current="1.4.1" platform="android" offer={offer} />);
      expect(offer).not.toHaveBeenCalled();
      expect(container).toBeEmptyDOMElement();
    } finally {
      vi.unstubAllEnvs();
    }
  });
});

describe("AppUpdateSettings", () => {
  beforeEach(() => localStorage.clear());

  it("sets how often the sideloaded app looks for a new version, and is absent elsewhere", async () => {
    wrap(<AppUpdateSettings platform="android" dist="sideload" />);
    expect(screen.getByRole("radio", { name: "Daily" })).toBeChecked();
    await userEvent.click(screen.getByRole("radio", { name: "Never" }));
    expect(localStorage.getItem("mkt:update:frequency")).toBe("off");

    for (const [platform, dist] of [
      ["android", "store"],
      ["ios", undefined],
      ["web", "sideload"],
    ] as const) {
      const other = wrap(<AppUpdateSettings platform={platform} dist={dist} />);
      expect(other.container).toBeEmptyDOMElement();
      other.unmount();
    }
  });
});
