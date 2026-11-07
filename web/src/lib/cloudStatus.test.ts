// Epic 50 story 50.11: the cloud's line in the status strip (T05, T08).
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cloudLine,
  refreshCloudStatus,
  setCloudStatus,
  STATUS_SITE,
  throughCloud,
  type CloudSummary,
} from "./cloudStatus";

const ok: CloudSummary = {
  updated_at: "2026-11-03T10:15:00Z",
  overall: "operational",
  components: { relay: "operational", api: "operational", push: "operational" },
  incident: null,
  maintenance: [],
};
const fmt = {
  date: (d: Date) => d.toISOString().slice(0, 10),
  time: (d: Date) => d.toISOString().slice(11, 16),
};
const now = Date.parse("2026-11-03T12:00:00Z");

afterEach(() => {
  setCloudStatus(null);
  vi.restoreAllMocks();
});

describe("cloudLine (T05)", () => {
  it("is empty when everything works, or before the feed answered", () => {
    expect(cloudLine(ok, now, fmt)).toBeNull();
    expect(cloudLine(null, now, fmt)).toBeNull();
  });

  it("says remote access is down, linking to the incident", () => {
    const l = cloudLine(
      {
        ...ok,
        overall: "outage",
        components: { ...ok.components, relay: "outage" },
        incident: {
          id: "2026-11-03-relay-down",
          state: "investigating",
          components: ["relay"],
          title: { en: "Remote access is down", ar: "الوصول عن بُعد متوقف" },
          url: `${STATUS_SITE}/i/2026-11-03-relay-down`,
        },
      },
      now,
      fmt
    );
    expect(l).toEqual({
      key: "cloud:2026-11-03-relay-down",
      text: "banner.relay.outage",
      href: `${STATUS_SITE}/i/2026-11-03-relay-down`,
    });
  });

  it("puts down before slow, and the relay before the API", () => {
    const l = cloudLine(
      { ...ok, components: { relay: "degraded", api: "outage", push: "operational" } },
      now,
      fmt
    );
    expect(l?.text).toBe("banner.api.outage");
    expect(l?.href).toBe(STATUS_SITE);
    expect(
      cloudLine({ ...ok, components: { relay: "degraded", api: "degraded" } }, now, fmt)?.text
    ).toBe("banner.relay.degraded");
  });

  it("never shows for notifications alone", () => {
    expect(
      cloudLine({ ...ok, components: { ...ok.components, push: "outage" } }, now, fmt)
    ).toBeNull();
  });

  it("shows maintenance from 24 hours ahead until it ends, with its window", () => {
    const m = {
      id: "2026-11-02-db",
      starts_at: "2026-11-04T02:00:00Z",
      ends_at: "2026-11-04T02:30:00Z",
      components: ["relay", "api"],
      title: { en: "Database upgrade", ar: "ترقية قاعدة البيانات" },
      url: `${STATUS_SITE}/i/2026-11-02-db`,
    };
    const s = { ...ok, maintenance: [m] };
    expect(cloudLine(s, Date.parse("2026-11-03T01:59:00Z"), fmt)).toBeNull(); // over 24 h ahead
    expect(cloudLine(s, Date.parse("2026-11-03T02:00:00Z"), fmt)).toEqual({
      key: "cloud:2026-11-02-db",
      text: "banner.maintenance",
      vars: { date: "2026-11-04", start: "02:00", end: "02:30" },
      href: `${STATUS_SITE}/i/2026-11-02-db`,
    });
    expect(cloudLine(s, Date.parse("2026-11-04T02:10:00Z"), fmt)?.text).toBe("banner.maintenance");
    expect(cloudLine(s, Date.parse("2026-11-04T02:30:00Z"), fmt)).toBeNull(); // over
    // maintenance of notifications alone: nothing
    expect(
      cloudLine(
        { ...ok, maintenance: [{ ...m, components: ["push"] }] },
        Date.parse("2026-11-04T02:10:00Z"),
        fmt
      )
    ).toBeNull();
  });

  it("a new incident is a new line, so one closed comes back for the next", () => {
    const a = cloudLine({ ...ok, components: { relay: "degraded" } }, now, fmt);
    const b = cloudLine({ ...ok, components: { relay: "outage" } }, now, fmt);
    expect(a?.key).not.toBe(b?.key);
  });
});

describe("refreshCloudStatus (T08)", () => {
  it("reads summary.json with the minute in the address, no credentials", async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify(ok), { status: 200 }));
    await refreshCloudStatus(f as unknown as typeof fetch, Date.parse("2026-11-03T12:00:30Z"));
    expect(f).toHaveBeenCalledTimes(1);
    const [url, init] = f.mock.calls[0];
    expect(url).toBe(
      `${STATUS_SITE}/summary.json?t=${Math.floor(Date.parse("2026-11-03T12:00:30Z") / 60000)}`
    );
    expect(init.credentials).toBe("omit");
  });

  it("a 404, a timeout or a broken body changes nothing and throws nothing", async () => {
    for (const f of [
      vi.fn().mockResolvedValue(new Response("", { status: 404 })),
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
      vi.fn().mockResolvedValue(new Response("<html>", { status: 200 })),
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ nope: 1 }), { status: 200 })),
    ]) {
      await expect(refreshCloudStatus(f as unknown as typeof fetch)).resolves.toBeUndefined();
    }
  });
});

describe("throughCloud", () => {
  it("is the relay's address or the cloud's editions, never the LAN", () => {
    expect(throughCloud("skylark-12.relay.maktaba.tv", false)).toBe(true);
    expect(throughCloud("relay.maktaba.tv", false)).toBe(true);
    expect(throughCloud("192.168.1.20", false)).toBe(false);
    expect(throughCloud("maktaba.local", false)).toBe(false);
    expect(throughCloud("evilrelay.maktaba.tv", false)).toBe(false);
    expect(throughCloud("localhost", true)).toBe(true);
  });
});
