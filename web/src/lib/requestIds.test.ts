// Epic 53 story 53.08 T01: one id per action (calls within a second share it), a UUIDv7;
// the last 20 requests are kept by route pattern.
import { describe, expect, it, vi, afterEach } from "vitest";
import {
  ACTION_WINDOW_MS,
  KEPT_REQUESTS,
  actionRequestId,
  lastRequests,
  newAction,
  noteRequest,
  shortRef,
  uuidv7,
} from "./requestIds";
import { request, REQUEST_ID_HEADER } from "./api";

afterEach(() => {
  vi.restoreAllMocks();
  newAction();
});

describe("request ids", () => {
  it("makes UUIDv7s that sort by time", () => {
    const a = uuidv7(1_730_000_000_000);
    const b = uuidv7(1_730_000_000_001);
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(a < b).toBe(true);
    expect(shortRef(a)).toBe(a.slice(0, 8));
  });

  it("shares one id between the calls of one action", () => {
    newAction();
    const t = 1_730_000_000_000;
    const a = actionRequestId(t);
    expect(actionRequestId(t + 200)).toBe(a);
    expect(actionRequestId(t + 400 + ACTION_WINDOW_MS)).not.toBe(a);
    newAction();
    expect(actionRequestId(t + 500 + ACTION_WINDOW_MS)).not.toBe(a);
  });

  it("keeps the last 20 requests", () => {
    for (let i = 0; i < 25; i++)
      noteRequest({ id: `r${i}`, method: "GET", route: "/api/x", status: 200, at: "" });
    const kept = lastRequests();
    expect(kept).toHaveLength(KEPT_REQUESTS);
    expect(kept[0].id).toBe("r5");
  });

  it("sends the action's id on every call, and the parallel calls share it", async () => {
    newAction();
    const seen: string[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (_u, init) => {
      seen.push(new Headers((init as RequestInit).headers).get(REQUEST_ID_HEADER) ?? "");
      return new Response(JSON.stringify({}), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    await Promise.all([request("/api/libraries"), request("/api/videos?q=x")]);
    expect(seen).toHaveLength(2);
    expect(seen[0]).toMatch(/^[0-9a-f-]{36}$/);
    expect(seen[1]).toBe(seen[0]);
    expect(
      lastRequests()
        .slice(-2)
        .map((r) => r.route)
    ).toEqual(["/api/libraries", "/api/videos"]);
  });
});
