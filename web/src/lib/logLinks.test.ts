// Links from a failure to its logs (update-attempts.md): the window around it, the
// filters /admin/logs reads from its address, a machine's own log files, and the bundle.
import { describe, expect, it } from "vitest";
import {
  LOG_MARGIN_MS,
  inWindow,
  logWindow,
  logFilesURL,
  logsExportURL,
  logsLink,
  nodeLogsLink,
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

  it("builds the log page's, a machine's, its files' and the bundle's links", () => {
    expect(logsLink()).toBe("/admin/logs");
    expect(logsLink({ level: "error", since: "2026-10-24T05:28:00.000Z" })).toBe(
      "/admin/logs?level=error&since=2026-10-24T05%3A28%3A00.000Z"
    );
    // a joined machine's own log files, on the same page, in the window
    expect(
      nodeLogsLink("n 1", { since: "2026-10-24T05:28:00.000Z", until: "2026-10-24T05:33:00.000Z" })
    ).toBe(
      "/admin/logs?node=n+1&since=2026-10-24T05%3A28%3A00.000Z&until=2026-10-24T05%3A33%3A00.000Z"
    );
    expect(logsExportURL({ since: "2026-10-24T05:28:00.000Z", node: "n1" })).toBe(
      "/api/admin/logs/export?since=2026-10-24T05%3A28%3A00.000Z&node=n1"
    );
    expect(logFilesURL({ until: "2026-10-24T05:33:00.000Z", node: "n1", limit: 2000 })).toBe(
      "/api/admin/logs/files?until=2026-10-24T05%3A33%3A00.000Z&node=n1&limit=2000"
    );
    expect(logFilesURL({})).toBe("/api/admin/logs/files");
  });

  it("reads the filters from an address, leaving out a time that does not parse", () => {
    expect(
      readLogFilters(
        "?services=api&level=warn&since=2026-10-24T05:28:00Z&until=2026-10-24T05:33:00Z&node=n1"
      )
    ).toEqual({
      services: "api",
      level: "warn",
      since: "2026-10-24T05:28:00Z",
      until: "2026-10-24T05:33:00Z",
      node: "n1",
    });
    // an old desktop app's link searched for its version (q), which hid the failure's lines
    expect(readLogFilters("?q=0.3.0&since=yesterday")).toEqual({
      services: undefined,
      level: undefined,
      since: undefined,
      until: undefined,
      node: undefined,
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
