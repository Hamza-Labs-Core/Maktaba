// Story 16.5: nothing is collected before an opt-in; the pseudonym is made at
// opt-in and a fresh one after an opt-out; toggling off stops at once; "Forget my
// device" deletes every pseudonym this device used.
import { describe, it, expect, vi, beforeEach } from "vitest";

const { get, post, del } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), del: vi.fn() }));
vi.mock("../api", async () => {
  const actual = await vi.importActual<typeof import("../api")>("../api");
  return { ...actual, api: { ...actual.api, get, post, delete: del } };
});

import {
  forgetDevice,
  noteRoute,
  optIn,
  optOut,
  queuedForTests,
  reportError,
  resetTelemetryForTests,
  startTelemetry,
  telemetryDecided,
  track,
} from "./client";
import { allPseudonyms, makePseudonym, pseudonym, readConsent } from "./consent";

describe("telemetry client", () => {
  beforeEach(() => {
    resetTelemetryForTests();
    localStorage.clear();
    get.mockReset().mockResolvedValue({ items: [] });
    post.mockReset().mockResolvedValue(undefined);
    del.mockReset().mockResolvedValue(undefined);
  });

  it("makes 16-character base32 pseudonyms", () => {
    const a = makePseudonym();
    expect(a).toMatch(/^[A-Z2-7]{16}$/);
    expect(makePseudonym()).not.toBe(a);
  });

  it("collects nothing before an opt-in, nor after a decline", () => {
    startTelemetry("/library");
    track("APP_OPEN", { cold_start: true });
    reportError(new Error("boom"));
    noteRoute("/search");
    expect(queuedForTests()).toBe(0);
    expect(pseudonym()).toBeNull();
    optOut();
    expect(telemetryDecided()).toBe(true);
    expect(readConsent()?.granted).toBe(false);
    track("APP_OPEN");
    expect(queuedForTests()).toBe(0);
    expect(get).not.toHaveBeenCalled();
  });

  it("opting in makes a pseudonym, keeps it for the session, and queues events", () => {
    optIn("/library");
    const p = pseudonym();
    expect(p).toMatch(/^[A-Z2-7]{16}$/);
    startTelemetry();
    expect(pseudonym()).toBe(p);
    track("FEATURE_USED", { feature: "search", count: 2 });
    expect(queuedForTests()).toBe(1);
  });

  it("sends under the pseudonym, with only the fields a kind allows", async () => {
    vi.useFakeTimers();
    try {
      optIn("/search");
      track("FEATURE_USED", { feature: "search", count: 1, query: "fiqh" } as never);
      await vi.advanceTimersByTimeAsync(31_000);
    } finally {
      vi.useRealTimers();
    }
    expect(post).toHaveBeenCalledTimes(1);
    const [path, body] = post.mock.calls[0] as [
      string,
      { device_pseudonym: string; events: { event_kind: string; payload: object }[] },
    ];
    expect(path).toBe("/api/telemetry");
    expect(body.device_pseudonym).toBe(pseudonym());
    const kinds = body.events.map((e) => e.event_kind);
    expect(kinds).toContain("app.open");
    expect(kinds).toContain("feature.used");
    for (const e of body.events) expect(e.payload).not.toHaveProperty("query");
  });

  it("toggling off stops at once and rotates the pseudonym", () => {
    optIn();
    const first = pseudonym();
    track("APP_OPEN");
    optOut();
    expect(queuedForTests()).toBe(0);
    track("APP_OPEN");
    expect(queuedForTests()).toBe(0);
    expect(pseudonym()).toBeNull();
    optIn();
    expect(pseudonym()).not.toBe(first);
    // the old one is kept for "Forget my device"
    expect(allPseudonyms()).toContain(first);
  });

  it("Forget my device deletes every pseudonym this device used and empties the queue", async () => {
    optIn();
    const first = pseudonym() as string;
    optOut();
    optIn();
    const second = pseudonym() as string;
    track("APP_OPEN");
    await forgetDevice();
    expect(del).toHaveBeenCalledWith(`/api/telemetry/devices/${first}`);
    expect(del).toHaveBeenCalledWith(`/api/telemetry/devices/${second}`);
    expect(allPseudonyms()).not.toContain(first);
    expect(allPseudonyms()).not.toContain(second);
    // still opted in: it carries on under a fresh pseudonym, with nothing queued
    expect(pseudonym()).not.toBe(second);
    expect(queuedForTests()).toBe(0);
  });

  it("keeps a pseudonym whose delete failed, to try again", async () => {
    optIn();
    const p = pseudonym();
    optOut();
    del.mockRejectedValueOnce(Object.assign(new Error("down"), { status: 503 }));
    await expect(forgetDevice()).rejects.toThrow("down");
    expect(allPseudonyms()).toContain(p);
  });
});
