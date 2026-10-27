import { describe, it, expect, afterEach, vi } from "vitest";
import { ApiError } from "./api";
import { errorText, inputDetail, plainDetail, userErrorKind } from "./errorText";
import en from "../i18n/en.json";

const t = (k: string) => (en as Record<string, string>)[k] ?? k;
const problem = (status: number, detail?: string, title = "internal error") =>
  new ApiError({ type: "about:blank", title, status, ...(detail ? { detail } : {}) });

afterEach(() => vi.unstubAllGlobals());

describe("plainDetail", () => {
  it.each([
    "a playlist with this name already exists",
    "username taken",
    "admin scope not allowed",
  ])("passes a plain sentence: %s", (d) => expect(plainDetail(d)).toBe(true));

  it.each([
    "open /Volumes/media/index.m3u8: no such file or directory",
    "C:\\Users\\x\\file",
    "read ./data/x/y failed",
    'pq: duplicate key {"id": 1}',
    "dial tcp: lookup db: no such host",
    "line one\nline two",
    "x".repeat(201),
    "",
  ])("refuses %s", (d) => expect(plainDetail(d)).toBe(false));

  it("refuses what is not a string", () => {
    expect(plainDetail(undefined)).toBe(false);
    expect(plainDetail({ a: 1 })).toBe(false);
  });
});

describe("errorText", () => {
  it("never shows a 5xx's title or detail", () => {
    const e = problem(500, "open /Volumes/media/x.mp4: permission denied");
    expect(errorText(e, t)).toBe(en["error.kind.server"]);
  });

  it("shows a plain 400 / 409 / 422 detail; a 403's only where asked", () => {
    expect(errorText(problem(409, "username taken", "conflict"), t)).toBe("username taken");
    expect(errorText(problem(400, "name is required", "bad request"), t)).toBe("name is required");
    expect(inputDetail(problem(400, "open /etc/x/y: denied"))).toBeNull();
    const refused = problem(403, "a cloud sign-in cannot pair a device", "forbidden");
    expect(errorText(refused, t)).toBe(en["error.kind.noAccess"]);
    expect(inputDetail(refused, { refusals: true })).toBe("a cloud sign-in cannot pair a device");
    expect(inputDetail(problem(403, "open /a/b/c: denied"), { refusals: true })).toBeNull();
  });

  it("names each kind in the app's words", () => {
    expect(errorText(problem(401), t)).toBe(en["error.kind.signedOut"]);
    expect(errorText(problem(403), t)).toBe(en["error.kind.noAccess"]);
    expect(errorText(problem(404, "video not found"), t)).toBe(en["error.kind.notFound"]);
    expect(errorText(problem(503), t)).toBe(en["error.kind.busy"]);
    expect(errorText(new TypeError("Failed to fetch"), t)).toBe(en["error.kind.unreachable"]);
    expect(errorText(new Error("boom"), t)).toBe(en["error.kind.server"]);
  });

  it("a TypeError from bad data is not the network", () => {
    const e = new TypeError("Cannot read properties of null (reading 'map')");
    expect(userErrorKind(e)).toBe("server");
  });

  it("says offline when the browser has no network", () => {
    vi.stubGlobal("navigator", { ...navigator, onLine: false });
    expect(userErrorKind(new TypeError("Failed to fetch"))).toBe("offline");
    expect(errorText(new TypeError("NetworkError when attempting to fetch"), t)).toBe(
      en["error.kind.offline"]
    );
  });
});

describe("every error string is in both languages", () => {
  it("has the error.kind keys in en and ar", async () => {
    const ar = (await import("../i18n/ar.json")).default as Record<string, string>;
    for (const k of Object.keys(en).filter(
      (k) => k.startsWith("error.") || k.startsWith("notFound.")
    )) {
      expect(ar[k], k).toBeTruthy();
      expect(ar[k], k).not.toBe((en as Record<string, string>)[k]);
    }
  });
});
