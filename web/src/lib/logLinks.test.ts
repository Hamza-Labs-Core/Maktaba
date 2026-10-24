// Links from a failure to its logs (update-attempts.md): the window around it, the
// filters /admin/logs reads from its address, and the per-machine link.
import { describe, expect, it } from "vitest";
import {
  LOG_MARGIN_MS,
  inWindow,
  logWindow,
  logsLink,
  machineLogsLink,
  readLogFilters,
} from "./logLinks";

describe("logLinks", () => {
  it("windows a failure two minutes either side; an open one has no end", () => {
    expect(logWindow("2026-10-24T05:30:00Z", "2026-10-24T05:31:12Z")).toEqual({
      since: "2026-10-24T05:28:00.000Z",
      until: "2026-10-24T05:33:12.000Z",
    });
    expect(LOG_MARGIN_MS).toBe(120_000);
    expect(logWindow("2026-10-24T05:30:00Z")).toEqual({ since: "2026-10-24T05:28:00.000Z" });
    expect(logWindow("not a time", "")).toEqual({});
    expect(logWindow()).toEqual({});
  });

  it("builds the log page's and a machine's links", () => {
    expect(logsLink()).toBe("/admin/logs");
    expect(logsLink({ q: "studio pc", level: "error", since: "2026-10-24T05:28:00.000Z" })).toBe(
      "/admin/logs?q=studio+pc&level=error&since=2026-10-24T05%3A28%3A00.000Z"
    );
    expect(machineLogsLink("n 1", { since: "2026-10-24T05:28:00.000Z" })).toBe(
      "/admin/machines?logs=n+1&since=2026-10-24T05%3A28%3A00.000Z"
    );
  });

  it("reads the filters from an address, leaving out a time that does not parse", () => {
    expect(
      readLogFilters(
        "?q=boom&services=api&level=warn&since=2026-10-24T05:28:00Z&until=2026-10-24T05:33:00Z"
      )
    ).toEqual({
      q: "boom",
      services: "api",
      level: "warn",
      since: "2026-10-24T05:28:00Z",
      until: "2026-10-24T05:33:00Z",
    });
    expect(readLogFilters("?since=yesterday")).toEqual({
      q: undefined,
      services: undefined,
      level: undefined,
      since: undefined,
      until: undefined,
    });
  });

  it("keeps a line inside the window, and one without a readable time", () => {
    const w = { since: "2026-10-24T05:28:00Z", until: "2026-10-24T05:33:00Z" };
    expect(inWindow("2026-10-24T05:30:00Z", w)).toBe(true);
    expect(inWindow("2026-10-24T05:27:59Z", w)).toBe(false);
    expect(inWindow("2026-10-24T05:33:01Z", w)).toBe(false);
    expect(inWindow("", w)).toBe(true);
    expect(inWindow("2026-10-24T05:00:00Z", {})).toBe(true);
  });
});
