import { describe, it, expect, vi, afterEach } from "vitest";
import { ApiError } from "./api";
import { describeThrown, installErrorLog } from "./errorLog";

afterEach(() => vi.restoreAllMocks());

describe("errorLog", () => {
  it("describes an ApiError by its status only (lib/api logged the rest)", () => {
    const e = new ApiError({ type: "x", title: "t", status: 500, detail: "secret path /a/b" });
    expect(describeThrown(e)).toBe("ApiError (500)");
  });

  it("describes an Error by kind, a short message and its first frames", () => {
    const e = new TypeError("x".repeat(500));
    const line = describeThrown(e);
    expect(line.startsWith("TypeError: " + "x".repeat(200))).toBe(true);
    expect(line).not.toContain("x".repeat(201));
  });

  it("names a thrown non-error by its type, never its value", () => {
    expect(describeThrown({ password: "hunter2" })).toBe("object");
    expect(describeThrown(null)).toBe("null");
  });

  it("logs uncaught errors and unhandled rejections once installed", () => {
    const log = vi.spyOn(console, "error").mockImplementation(() => {});
    const undo = installErrorLog();
    const err = new ErrorEvent("error", { message: "bad" });
    Object.defineProperty(err, "error", { value: new RangeError("bad") });
    window.dispatchEvent(err);
    const ev = new Event("unhandledrejection") as PromiseRejectionEvent;
    Object.defineProperty(ev, "reason", { value: new Error("nobody caught me") });
    window.dispatchEvent(ev);
    undo();
    window.dispatchEvent(new ErrorEvent("error", { message: "after undo" }));
    const lines = log.mock.calls.map((c) => c.join(" "));
    expect(lines.some((l) => l.includes("[uncaught] error") && l.includes("RangeError: bad"))).toBe(
      true
    );
    expect(
      lines.some(
        (l) => l.includes("[uncaught] promise rejection") && l.includes("nobody caught me")
      )
    ).toBe(true);
    expect(lines.some((l) => l.includes("after undo"))).toBe(false);
  });
});

describe("the log ring (Epic 53 story 53.01)", () => {
  it("keeps the newest 2,000 lines in the shared shape, scrubbed", async () => {
    const { appLog, clearLogLines, logLines, RING_LINES, setLogAppVersion } = await import(
      "./errorLog"
    );
    clearLogLines();
    setLogAppVersion("v0.3.50");
    for (let i = 0; i < 3000; i++) appLog("info", "tick", { i });
    appLog("warn", "fetch_failed", { url: "/a?token=abc", who: "a@b.co" });
    const lines = logLines();
    expect(lines).toHaveLength(RING_LINES);
    expect(lines[0].attrs).toEqual({ i: 1001 });
    const last = lines[lines.length - 1];
    expect(last).toMatchObject({
      level: "warn",
      service: "web",
      msg: "fetch_failed",
      platform: "web",
      app_version: "v0.3.50",
    });
    expect(last.attrs).toEqual({ url: "/a?token=[REDACTED]", who: "[EMAIL]" });
    expect(last.ts).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/);
    clearLogLines();
  });

  it("logs a route by its pattern and an API failure without its query", async () => {
    const { clearLogLines, logApiFailure, logLines, logRouteChanged, routePattern } = await import(
      "./errorLog"
    );
    clearLogLines();
    logRouteChanged(routePattern("/videos/01J9ZK2/watch"));
    logApiFailure("GET", "/api/videos/01J9ZK2?q=secret", 502);
    expect(logLines().map((l) => [l.level, l.msg, l.attrs])).toEqual([
      ["info", "route_changed", { route: "/videos/:id/watch" }],
      ["error", "api_failed", { method: "GET", path: "/api/videos/:id", status: 502 }],
    ]);
    clearLogLines();
  });
});
