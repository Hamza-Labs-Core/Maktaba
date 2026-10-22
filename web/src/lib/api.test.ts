// request(): how a failed response becomes an ApiError; downloadBlob decodes
// the same way; a node's home outage as lib/api records it (PR #145).
import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import {
  HOME_STATE_HEADER,
  NODE_AVAILABLE_EVENT,
  api,
  ApiError,
  clearNodeUnavailable,
  downloadBlob,
  homeUnreachable,
  isHomeUnreachable,
  lastNodeUnavailable,
  noteHomeBack,
} from "./api";

function answer(status: number, body: string, type: string) {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(body, { status, statusText: "Bad Gateway", headers: { "Content-Type": type } })
    )
  );
}

async function failure(): Promise<ApiError> {
  try {
    await api.get("/api/x");
  } catch (e) {
    return e as ApiError;
  }
  throw new Error("expected a failure");
}

describe("api request errors", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("REG-11 / RPW-10: an HTML error page is not put in problem.detail", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    answer(502, "<html><body>Bad Gateway</body></html>", "text/html");
    const err = await failure();
    expect(err.status).toBe(502);
    expect(err.problem.detail).toBeUndefined();
    expect(err.message).not.toMatch(/<html>/);
  });

  it("keeps a plain-text error body as the detail", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    answer(400, "username too short", "text/plain; charset=utf-8");
    expect((await failure()).problem.detail).toBe("username too short");
  });
});

describe("a node's home outage", () => {
  beforeEach(() => clearNodeUnavailable());
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    clearNodeUnavailable();
  });

  const unreachable = (extra: Record<string, unknown> = {}) =>
    new Response(
      JSON.stringify({
        title: "the home is not reachable",
        status: 503,
        code: "home_unreachable",
        ...extra,
      }),
      { status: 503, headers: { "Content-Type": "application/problem+json" } }
    );

  it("sends every change while the home is unreachable: the node decides", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const fetchMock = vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) =>
      init?.method === "DELETE"
        ? new Response(null, { status: 204, headers: { [HOME_STATE_HEADER]: "unreachable" } })
        : unreachable()
    );
    vi.stubGlobal("fetch", fetchMock);
    await expect(api.post("/api/videos/v1/tags", { tag: "x" })).rejects.toSatisfy(
      isHomeUnreachable
    );
    expect(homeUnreachable()).toBe(true);
    // a local route (playback teardown) still reaches the node, which serves it
    await expect(api.delete("/api/stream/sessions/s1")).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("downloadBlob decodes a problem answer and the home state like request()", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => unreachable({ readable: false }))
    );
    const err = await downloadBlob("/api/diagnostics/bundle", "x.tar.gz").catch((e: unknown) => e);
    expect(isHomeUnreachable(err)).toBe(true);
    expect((err as ApiError).problem.title).toBe("the home is not reachable");
    expect(lastNodeUnavailable()).toEqual({ code: "home_unreachable", readable: false });

    clearNodeUnavailable();
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response("no", { status: 500, headers: { [HOME_STATE_HEADER]: "unreachable" } })
      )
    );
    const plain = await downloadBlob("/api/diagnostics/bundle", "x.tar.gz").catch(
      (e: unknown) => e
    );
    expect((plain as ApiError).problem.detail).toBe("no");
    expect(homeUnreachable()).toBe(true);
  });

  it("keeps the graver answer, and the home coming back forgets only home_unreachable", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => unreachable({ readable: false }))
    );
    await api.get("/api/videos").catch(() => {});
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("{}", { headers: { [HOME_STATE_HEADER]: "unreachable" } }))
    );
    await api.get("/api/videos");
    expect(lastNodeUnavailable()).toEqual({ code: "home_unreachable", readable: false });

    const back = vi.fn();
    window.addEventListener(NODE_AVAILABLE_EVENT, back);
    noteHomeBack();
    expect(homeUnreachable()).toBe(false);
    expect(back).toHaveBeenCalledTimes(1);

    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({ title: "paused", status: 503, code: "paused", reason: "r" }),
            {
              status: 503,
              headers: { "Content-Type": "application/problem+json" },
            }
          )
      )
    );
    await api.get("/api/videos").catch(() => {});
    noteHomeBack(); // a pause is not the home's to clear
    expect(lastNodeUnavailable()).toEqual({ code: "paused", reason: "r" });
    expect(back).toHaveBeenCalledTimes(1);
    window.removeEventListener(NODE_AVAILABLE_EVENT, back);
  });
});
