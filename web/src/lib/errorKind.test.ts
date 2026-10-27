// The shared error kinds: every client sorts a failure the same way.
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, api, clearNodeUnavailable, retryAfterSeconds } from "./api";
import {
  classifyError,
  classifyPlayback,
  errorStateKind,
  kindOfStatus,
  problemSlug,
} from "./errorKind";

const RENDITION_NOT_FOUND = {
  type: "https://maktaba.invalid/problems/rendition-not-found",
  title: "rendition index not on disk",
  status: 404,
  detail: "open /Volumes/x/index.m3u8: no such file or directory",
};

describe("problemSlug", () => {
  it("is the part after /problems/, whatever the domain", () => {
    expect(problemSlug("https://maktaba.invalid/problems/rendition-not-found")).toBe(
      "rendition-not-found"
    );
    expect(problemSlug("https://maktaba.dev/problems/home-unreachable")).toBe("home-unreachable");
    expect(problemSlug("about:blank")).toBe("");
    expect(problemSlug(undefined)).toBe("");
  });
});

describe("classifyError", () => {
  it("a rendition not on disk yet is notReady, its words kept for Details only", () => {
    const c = classifyError(new ApiError(RENDITION_NOT_FOUND));
    expect(c).toEqual({
      kind: "notReady",
      status: 404,
      slug: "rendition-not-found",
      title: "rendition index not on disk",
      detail: "open /Volumes/x/index.m3u8: no such file or directory",
    });
  });

  it.each([
    ["manifest-not-found", 404, undefined, "notReady"],
    ["video-not-found", 404, undefined, "notFound"],
    ["", 404, undefined, "notFound"],
    ["", 410, undefined, "notFound"],
    ["", 401, undefined, "signedOut"],
    ["", 401, "signin_required", "signedOut"],
    ["", 403, undefined, "noAccess"],
    ["", 503, "home_unreachable", "unreachable"],
    ["", 504, "home_timeout", "unreachable"],
    ["home-unreachable", 502, undefined, "unreachable"],
    ["video-unavailable-offline", 503, "video_unavailable_offline", "unreachable"],
    ["", 503, "paused", "busy"],
    ["", 503, undefined, "busy"],
    ["", 429, undefined, "busy"],
    ["", 500, undefined, "server"],
    ["insufficient-storage", 507, "insufficient_storage", "server"],
    ["", 422, undefined, "server"],
  ])("%s %i (code %s) is %s", (slug, status, code, kind) => {
    const e = new ApiError({
      type: slug ? `https://maktaba.dev/problems/${slug}` : "about:blank",
      title: "t",
      status,
      ...(code ? { code } : {}),
    });
    expect(classifyError(e).kind).toBe(kind);
  });

  it("busy carries the Retry-After seconds", () => {
    const c = classifyError(
      new ApiError({ type: "about:blank", title: "slow down", status: 429 }, 7)
    );
    expect(c).toMatchObject({ kind: "busy", retryAfter: 7 });
    const s = classifyError(new ApiError({ type: "about:blank", title: "busy", status: 503 }, 30));
    expect(s).toMatchObject({ kind: "busy", retryAfter: 30 });
    // only busy carries it
    expect(
      classifyError(new ApiError({ type: "about:blank", title: "x", status: 500 }, 5)).retryAfter
    ).toBeUndefined();
  });

  it("no answer at all (fetch's TypeError) is unreachable; anything else is server", () => {
    for (const m of [
      "Failed to fetch",
      "NetworkError when attempting to fetch resource.",
      "Load failed",
      "fetch failed",
    ]) {
      expect(classifyError(new TypeError(m)).kind).toBe("unreachable");
    }
    // bad data in an answer is a TypeError too, and not the network
    expect(
      classifyError(new TypeError("Cannot read properties of undefined (reading 'items')")).kind
    ).toBe("server");
    expect(classifyError(new SyntaxError("Unexpected token <")).kind).toBe("server");
    expect(classifyError("boom").kind).toBe("server");
    expect(classifyError(null)).toMatchObject({ kind: "server", status: 0 });
  });

  it("kindOfStatus with no problem at all", () => {
    expect(kindOfStatus(0)).toBe("unreachable");
    expect(kindOfStatus(502)).toBe("server");
  });
});

describe("classifyPlayback", () => {
  it.each([
    [{ code: "manifestLoadError", httpStatus: 404 }, "notReady"],
    [{ code: "levelLoadError", httpStatus: 404 }, "notReady"],
    [{ code: "manifest_gone", httpStatus: 404 }, "notReady"],
    [{ code: "fragLoadError", httpStatus: 410 }, "server"],
    [{ code: "fragLoadError", httpStatus: 403 }, "noAccess"],
    [{ code: "fragLoadError", httpStatus: 503 }, "busy"],
    [{ code: "fragLoadError", httpStatus: 0 }, "unreachable"],
    [{ code: "manifestLoadTimeOut" }, "unreachable"],
    [{ code: "media_err_2" }, "unreachable"],
    [{ code: "media_err_3", httpStatus: 200 }, "server"],
    [{ code: "bufferAppendError", httpStatus: 0 }, "server"],
  ])("%o is %s", (f, kind) => {
    expect(classifyPlayback(f).kind).toBe(kind);
  });
  it("nothing heard of is server", () => {
    expect(classifyPlayback(null).kind).toBe("server");
  });
});

describe("a malformed problem", () => {
  it("keeps no title or detail that is not a string", () => {
    const e = new ApiError({
      type: "about:blank",
      title: {} as unknown as string,
      status: 500,
      detail: 7 as unknown as string,
    });
    expect(classifyError(e)).toMatchObject({ kind: "server", title: "", detail: "" });
  });
});

describe("errorStateKind", () => {
  it("maps onto the design system's categories", () => {
    expect(errorStateKind("notFound")).toBe("not_found");
    expect(errorStateKind("noAccess")).toBe("permission");
    expect(errorStateKind("signedOut")).toBe("permission");
    expect(errorStateKind("unreachable")).toBe("network");
    expect(errorStateKind("notReady")).toBe("server");
    expect(errorStateKind("busy")).toBe("server");
  });
});

describe("Retry-After on an ApiError", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    clearNodeUnavailable();
  });

  it("reads delta-seconds and HTTP dates", () => {
    expect(retryAfterSeconds("12")).toBe(12);
    expect(retryAfterSeconds(null)).toBeUndefined();
    expect(retryAfterSeconds("soon")).toBeUndefined();
    const at = new Date(Date.now() + 9_500).toUTCString();
    const s = retryAfterSeconds(at);
    expect(s).toBeGreaterThanOrEqual(8);
    expect(s).toBeLessThanOrEqual(10);
  });

  it.each([
    [
      "problem+json",
      "application/problem+json",
      '{"type":"about:blank","title":"busy","status":503}',
    ],
    ["plain text", "text/plain", "busy"],
  ])("a %s 503 keeps its Retry-After", async (_l, type, body) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(body, {
            status: 503,
            headers: { "Content-Type": type, "Retry-After": "20" },
          })
      )
    );
    vi.spyOn(console, "error").mockImplementation(() => {});
    const e = await api.get("/api/x").catch((x: unknown) => x);
    expect(e).toBeInstanceOf(ApiError);
    expect((e as ApiError).retryAfter).toBe(20);
    expect(classifyError(e)).toMatchObject({ kind: "busy", retryAfter: 20 });
  });
});
