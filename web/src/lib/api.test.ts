// request(): how a failed response becomes an ApiError.
import { describe, it, expect, vi, afterEach } from "vitest";
import { api, ApiError } from "./api";

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
