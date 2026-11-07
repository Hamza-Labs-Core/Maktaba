// Epic 53 story 53.14: nothing leaves the device while crash reports are off; when on, an
// error goes once per fingerprint per page load, as an envelope with the consent in its
// header, scrubbed, with the app's lines.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  CONSENT,
  crashReportsAsked,
  crashReportsOn,
  envelope,
  reportCrash,
  resetCrashReportsForTest,
  sendShellCrashes,
  setCrashReports,
  type ShellCrash,
} from "./crashReports";
import { appLog, clearLogLines } from "./errorLog";

describe("crash reports to the home", () => {
  const fetchMock = vi.fn();
  beforeEach(() => {
    localStorage.clear();
    resetCrashReportsForTest();
    clearLogLines();
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    fetchMock.mockReset();
  });

  it("is off and unasked until the person answers", async () => {
    expect(crashReportsOn()).toBe(false);
    expect(crashReportsAsked()).toBe(false);
    expect(await reportCrash(new Error("boom"))).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
    setCrashReports(false);
    expect(crashReportsAsked()).toBe(true);
    expect(await reportCrash(new Error("boom"))).toBe(false);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("when on, sends an envelope once per fingerprint, with the consent, scrubbed", async () => {
    setCrashReports(true);
    appLog("info", "page_opened", { path: "/videos/1?token=abc" });
    expect(await reportCrash(new TypeError("x is undefined at row 12; token=abc"))).toBe(true);
    expect(await reportCrash(new TypeError("x is undefined at row 13; token=abc"))).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/api/diagnostics/envelope");
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe(
      "application/x-sentry-envelope"
    );
    const body = String(init.body);
    const [header, item] = body.split("\n");
    expect(JSON.parse(header).consent).toBe(CONSENT);
    expect(JSON.parse(item).type).toBe("event");
    expect(body).not.toContain("token=abc");
    expect(body).toContain("page_opened");
  });

  it("an envelope's items carry their byte lengths", () => {
    const env = envelope("Error: بدأ ثم توقف", "Error\n    at f (a.js:1:1)", "0.3.84");
    const lines = env.split("\n");
    const item = JSON.parse(lines[1]);
    expect(new TextEncoder().encode(lines[2]).length).toBe(item.length);
    expect(JSON.parse(lines[0]).app_version).toBe("0.3.84");
  });

  // the desktop shell's own panics (apps/desktop/src-tauri/src/crashes.rs)
  const shellCrash = (id: string): ShellCrash => ({
    id,
    at: 1_800_000_000_000,
    thread: "main",
    message: "called unwrap on Authorization: Bearer abcdef.ghijkl",
    location: "src/tray.rs:12",
    app_version: "0.3.90",
    platform: "macos",
    log: ['{"msg":"panic","attrs":{"text":"mail someone@example.com"}}'],
  });

  it("sends nothing of the shell's crashes while off, and never asks the shell", async () => {
    const invoke = vi.fn();
    expect(await sendShellCrashes(invoke)).toBe(0);
    expect(invoke).not.toHaveBeenCalled();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("sends the shell's crashes when on and tells the shell which went", async () => {
    setCrashReports(true);
    fetchMock.mockReset();
    fetchMock
      .mockResolvedValueOnce(new Response("{}", { status: 200 }))
      .mockResolvedValueOnce(new Response("{}", { status: 429 }));
    const invoke = vi.fn(async (cmd: string) =>
      cmd === "shell_crashes" ? [shellCrash("a"), shellCrash("b")] : 1
    );
    expect(await sendShellCrashes(invoke as never)).toBe(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const body = String(fetchMock.mock.calls[0][1].body);
    const header = JSON.parse(body.split("\n")[0]);
    expect(header.consent).toBe(CONSENT);
    expect(header.app).toBe("desktop");
    expect(body).not.toContain("abcdef");
    expect(body).not.toContain("someone@example.com");
    expect(body).toContain("src/tray.rs:12");
    // only the one the home took; the other is offered again at the next start
    expect(invoke).toHaveBeenLastCalledWith("shell_crashes_sent", { ids: ["a"] });
  });
});
