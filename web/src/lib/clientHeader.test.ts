// X-Maktaba-Client (Epic 44 story 44.2): which app, version, platform and
// device every request names, and that every way the web calls the API sends it.
import { describe, it, expect, vi, afterEach } from "vitest";
import {
  CLIENT_HEADER,
  clientDevice,
  clientHeaderValue,
  clientParts,
  clientPlatform,
  formatClient,
  resetClientHeaderForTest,
} from "./clientHeader";
import { api, csrfHeaders, deleteBeacon, downloadBlob, postBeacon } from "./api";

const IPHONE = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148";
const IPAD = "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) Mobile/15E148";
const ANDROID_PHONE = "Mozilla/5.0 (Linux; Android 14; Pixel 8) Mobile Safari/537.36";
const ANDROID_TABLET = "Mozilla/5.0 (Linux; Android 14; SM-X710) Safari/537.36";
const MAC = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15";
const WINDOWS = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0";

describe("X-Maktaba-Client value", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    resetClientHeaderForTest();
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
    delete (window as unknown as Record<string, unknown>).Capacitor;
  });

  it("names the platform the server knows", () => {
    expect(clientPlatform(IPHONE)).toBe("ios");
    expect(clientPlatform(IPAD)).toBe("ipados");
    expect(clientPlatform(ANDROID_PHONE)).toBe("android");
    expect(clientPlatform(MAC)).toBe("macos");
    expect(clientPlatform(WINDOWS)).toBe("windows");
    expect(clientPlatform("Mozilla/5.0 (X11; Linux x86_64)")).toBe("linux");
    expect(clientPlatform("")).toBe("web");
  });

  it("names the device", () => {
    expect(clientDevice(IPHONE, "web")).toBe("phone");
    expect(clientDevice(IPAD, "web")).toBe("tablet");
    expect(clientDevice(ANDROID_PHONE, "mobile")).toBe("phone");
    expect(clientDevice(ANDROID_TABLET, "mobile")).toBe("tablet");
    expect(clientDevice(MAC, "web")).toBe("desktop");
    expect(clientDevice(IPHONE, "desktop")).toBe("desktop");
  });

  it("is web in a browser, desktop in the Tauri shell, mobile in Capacitor", () => {
    expect(clientParts(MAC, "0.3.25")).toEqual({
      app: "web",
      version: "0.3.25",
      platform: "macos",
      device: "desktop",
    });
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {};
    expect(clientParts(WINDOWS, "0.3.25").app).toBe("desktop");
    delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
    (window as unknown as Record<string, unknown>).Capacitor = { isNativePlatform: () => true };
    expect(formatClient(clientParts(ANDROID_PHONE, "0.3.25"))).toBe(
      "mobile/0.3.25 (android; phone)"
    );
    expect(clientParts(MAC, "").version).toBe("dev");
  });

  it("has the server's format", () => {
    expect(clientHeaderValue()).toMatch(/^web\/[^ ]+ \([a-z]+; [a-z]+\)$/);
  });
});

describe("every API call sends X-Maktaba-Client", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const sent = (call: unknown[]) =>
    new Headers((call[1] as RequestInit)?.headers).get(CLIENT_HEADER);

  it("request, downloadBlob, the beacons and hand-rolled fetches", async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })
    );
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal(
      "URL",
      class extends URL {
        static createObjectURL = () => "blob:x";
        static revokeObjectURL = () => {};
      }
    );
    await api.get("/api/x");
    await api.post("/api/x", { a: 1 });
    await downloadBlob("/api/x", "x.bin");
    postBeacon("/api/x", {});
    deleteBeacon("/api/x");
    const want = clientHeaderValue();
    expect(fetchMock.mock.calls.length).toBe(5);
    for (const call of fetchMock.mock.calls) expect(sent(call)).toBe(want);
    expect(csrfHeaders()[CLIENT_HEADER]).toBe(want);
  });
});
