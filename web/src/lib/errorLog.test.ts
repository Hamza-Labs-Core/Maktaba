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
