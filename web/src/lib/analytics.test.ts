import { describe, expect, it } from "vitest";
import {
  formatPercent,
  formatPercentRatio,
  formatWatchTime,
  platformFromUA,
} from "./analytics";

describe("formatWatchTime", () => {
  it("renders zero and sub-minute as 0m", () => {
    expect(formatWatchTime(0)).toBe("0m");
    expect(formatWatchTime(-5)).toBe("0m");
  });
  it("renders minutes under an hour", () => {
    expect(formatWatchTime(90)).toBe("2m"); // rounds
    expect(formatWatchTime(1800)).toBe("30m");
  });
  it("renders hours and minutes", () => {
    expect(formatWatchTime(3600)).toBe("1h");
    expect(formatWatchTime(3660)).toBe("1h 1m");
    expect(formatWatchTime(7320)).toBe("2h 2m");
  });
});

describe("percent formatters", () => {
  it("formatPercent rounds a 0..100 value", () => {
    expect(formatPercent(0)).toBe("0%");
    expect(formatPercent(47.5)).toBe("48%");
    expect(formatPercent(100)).toBe("100%");
  });
  it("formatPercentRatio scales a 0..1 ratio", () => {
    expect(formatPercentRatio(0)).toBe("0%");
    expect(formatPercentRatio(0.4)).toBe("40%");
    expect(formatPercentRatio(1)).toBe("100%");
  });
});

describe("platformFromUA", () => {
  it("classifies the common shells", () => {
    expect(platformFromUA("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)")).toBe("ios");
    expect(platformFromUA("Mozilla/5.0 (iPad; CPU OS 17_0)")).toBe("ios");
    expect(platformFromUA("Mozilla/5.0 (Linux; Android 14; Pixel)")).toBe("android");
    expect(platformFromUA("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15)")).toBe("macos");
    expect(platformFromUA("Mozilla/5.0 (Windows NT 10.0; Win64; x64)")).toBe("windows");
    expect(platformFromUA("Mozilla/5.0 (X11; Linux x86_64)")).toBe("linux");
    expect(platformFromUA("something weird")).toBe("browser");
  });
});
