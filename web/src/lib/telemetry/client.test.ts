// Story 16.5: nothing is collected before this app's user agrees; the install id
// is made at opt-in and a fresh one after an opt-out; toggling off stops at once;
// "Delete what this app sent" deletes under every id this app used. The batch
// goes to the home's pipe with the consent in it, never to the cloud directly,
// and asks nothing about the libraries.
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

  it("makes 24-character base32 install ids", () => {
    const a = makePseudonym();
    expect(a).toMatch(/^[A-Z2-7]{24}$/);
    expect(makePseudonym()).not.toBe(a);
  });

  it("forgets a yes given when the data went to the home's admin", async () => {
    localStorage.setItem("mkt:telemetry:consent", JSON.stringify({ granted: true, at: "x" }));
    localStorage.setItem("mkt:telemetry:pseudonym", "ABCDEFGHJKMNPQRS");
    vi.resetModules();
    const fresh = await import("./client");
    expect(fresh.telemetryDecided()).toBe(false);
    expect(fresh.telemetryEnabled()).toBe(false);
    expect(localStorage.getItem("mkt:telemetry:pseudonym")).toBeNull();
  });

  it("collects nothing before an opt-in, nor after a decline", () => {
    startTelemetry();
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

  it("opting in makes an install id, keeps it for the session, and queues events", () => {
    optIn();
    const p = pseudonym();
    expect(p).toMatch(/^[A-Z2-7]{24}$/);
    startTelemetry();
    expect(pseudonym()).toBe(p);
    track("FEATURE_USED", { feature: "search", count: 2 });
    expect(queuedForTests()).toBe(2); // app.open, once a launch, and this
  });

  it("sends to the home's pipe with the consent, under the install id, with only the fields a kind allows", async () => {
    vi.useFakeTimers();
    try {
      optIn();
      track("FEATURE_USED", { feature: "search", count: 1, query: "fiqh" } as never);
      await vi.advanceTimersByTimeAsync(31_000);
    } finally {
      vi.useRealTimers();
    }
    expect(post).toHaveBeenCalledTimes(1);
    const [path, body] = post.mock.calls[0] as [
      string,
      {
        consent: boolean;
        install_id: string;
        app: string;
        platform: string;
        events: { kind: string; payload: object }[];
      },
    ];
    expect(path).toBe("/api/telemetry/app");
    expect(body.consent).toBe(true);
    expect(body.install_id).toBe(pseudonym());
    expect(body.app).toBe("web");
    expect(body.platform).toMatch(/^[a-z0-9._-]+$/);
    expect(get).not.toHaveBeenCalled(); // no library requests
    const kinds = body.events.map((e) => e.kind);
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
    // the old one is kept for "Delete what this app sent"
    expect(allPseudonyms()).toContain(first);
  });

  it("Delete what this app sent covers every id this app used and empties the queue", async () => {
    optIn();
    const first = pseudonym() as string;
    optOut();
    optIn();
    const second = pseudonym() as string;
    track("APP_OPEN");
    await forgetDevice();
    expect(del).toHaveBeenCalledWith(`/api/telemetry/app/${first}`);
    expect(del).toHaveBeenCalledWith(`/api/telemetry/app/${second}`);
    expect(allPseudonyms()).not.toContain(first);
    expect(allPseudonyms()).not.toContain(second);
    // still opted in: it carries on under a fresh id, with nothing queued
    expect(pseudonym()).not.toBe(second);
    expect(queuedForTests()).toBe(0);
  });

  it("keeps an id whose delete failed, to try again", async () => {
    optIn();
    const p = pseudonym();
    optOut();
    del.mockRejectedValueOnce(Object.assign(new Error("down"), { status: 503 }));
    await expect(forgetDevice()).rejects.toThrow("down");
    expect(allPseudonyms()).toContain(p);
  });
});

describe("the hard rule", () => {
  beforeEach(() => {
    resetTelemetryForTests();
    localStorage.clear();
    post.mockReset().mockResolvedValue(undefined);
  });

  it("an error is sent as its kind, never its message", async () => {
    vi.useFakeTimers();
    try {
      optIn();
      reportError(new TypeError("cannot read 'أسماء الله الحسنى' of /Users/sara/x.mp4"));
      await vi.advanceTimersByTimeAsync(31_000);
    } finally {
      vi.useRealTimers();
    }
    const body = JSON.stringify(post.mock.calls[0][1]);
    expect(body).toContain('"error_message":"TypeError"');
    for (const bad of ["أسماء", "sara", "x.mp4", "cannot read"]) expect(body).not.toContain(bad);
  });

  it("the batch names no user, home, device or address", async () => {
    vi.useFakeTimers();
    try {
      optIn();
      await vi.advanceTimersByTimeAsync(31_000);
    } finally {
      vi.useRealTimers();
    }
    const body = post.mock.calls[0][1] as Record<string, unknown>;
    expect(Object.keys(body).sort()).toEqual(
      [
        "app",
        "app_version",
        "consent",
        "events",
        "form_factor",
        "install_id",
        "locale",
        "os_version",
        "platform",
      ].sort()
    );
  });
});
