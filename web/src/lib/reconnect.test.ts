// Epic 50 story 50.13 (T05): a home reconnecting behind the relay is waited for.
import { describe, expect, it, vi } from "vitest";
import { delays, isReconnecting, rideOutReconnect } from "./reconnect";
import { ApiError } from "./api";
import { classifyError } from "./errorKind";

const reconnecting = () =>
  new Response(JSON.stringify({ error: "home_reconnecting", message: "" }), {
    status: 503,
    headers: { "Content-Type": "application/json", "Retry-After": "5" },
  });
const ok = () => new Response(JSON.stringify({ items: [] }), { status: 200 });

describe("riding out a reconnect", () => {
  it("waits 2, 4, 8, then 15 s while the 60 s last", () => {
    expect(delays()).toEqual([2000, 4000, 8000, 15000, 15000, 15000]);
    expect(delays().reduce((a, b) => a + b, 0)).toBeLessThanOrEqual(60_000);
  });

  it("three home_reconnecting answers, then the page loads", async () => {
    const answers = [reconnecting(), reconnecting(), reconnecting(), ok()];
    const send = vi.fn(async () => answers.shift() as Response);
    const slept: number[] = [];
    const res = await rideOutReconnect(send, async (ms) => {
      slept.push(ms);
    });
    expect(res.status).toBe(200);
    expect(send).toHaveBeenCalledTimes(4);
    expect(slept).toEqual([2000, 4000, 8000]);
  });

  it("gives up after 60 s and hands back the last answer", async () => {
    const send = vi.fn(async () => reconnecting());
    const res = await rideOutReconnect(send, async () => {});
    expect(res.status).toBe(503);
    expect(send).toHaveBeenCalledTimes(delays().length + 1);
  });

  it("never retries 502 server_offline, another 503, or a success", async () => {
    for (const r of [
      new Response(JSON.stringify({ error: "server_offline" }), { status: 502 }),
      new Response(JSON.stringify({ code: "paused" }), { status: 503 }),
      new Response("<html>", { status: 503 }),
      ok(),
    ]) {
      const send = vi.fn(async () => r);
      await rideOutReconnect(send, async () => {});
      expect(send).toHaveBeenCalledTimes(1);
    }
    expect(await isReconnecting(reconnecting())).toBe(true);
  });

  it("after the retries, the error reads as the home unreachable", () => {
    const e = new ApiError({
      type: "about:blank",
      title: "",
      status: 503,
      error: "home_reconnecting",
    } as never);
    expect(classifyError(e).kind).toBe("unreachable");
  });
});
