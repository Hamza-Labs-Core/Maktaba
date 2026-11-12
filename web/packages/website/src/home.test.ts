// @vitest-environment node
// Story 60.10 T03's logic: the visitor's system from its user agent, and the Download link it
// preselects. The page itself (each system's agent, the button's label and link) is
// e2e/home.spec.ts's.
import { describe, expect, it } from "vitest";
import { platformOf, preselect } from "./home";

const AGENTS = {
  windows:
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
  mac: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
  linux: "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
  ios: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1",
  android:
    "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
} as const;

describe("the visitor's system", () => {
  it("is read from the user agent of each of the five systems", () => {
    for (const [want, ua] of Object.entries(AGENTS)) expect(platformOf(ua), want).toBe(want);
  });

  it("is iOS for an iPad that says Macintosh but has a touch screen", () => {
    expect(platformOf(AGENTS.mac, "", 5)).toBe("ios");
  });

  it("follows the client-hint platform when the browser gives it", () => {
    expect(platformOf("Mozilla/5.0", "Windows")).toBe("windows");
    expect(platformOf("Mozilla/5.0", "macOS")).toBe("mac");
  });

  it("is unknown when the agent hides it, and for ChromeOS", () => {
    expect(platformOf("")).toBeNull();
    expect(platformOf("Mozilla/5.0 (compatible; Googlebot/2.1)")).toBeNull();
    expect(platformOf("Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) Chrome/131.0.0.0")).toBeNull();
  });
});

describe("the Download link", () => {
  it("gets the system as its fragment, in either language", () => {
    expect(preselect("/download/", "mac")).toBe("/download/#mac");
    expect(preselect("/ar/download/", "android")).toBe("/ar/download/#android");
    expect(preselect("/download/#linux", "windows")).toBe("/download/#windows");
  });

  it("is left alone with no system, or when it goes elsewhere (the release, before 60.11)", () => {
    expect(preselect("/download/", null)).toBe("/download/");
    const release = "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest";
    expect(preselect(release, "mac")).toBe(release);
  });
});
