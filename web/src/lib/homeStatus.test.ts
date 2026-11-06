// Epic 53 story 53.06 T01-T03b: the strip's lines for each status, in priority order; a
// failed read changes nothing; off is no outage.
import { describe, expect, it, vi, afterEach } from "vitest";
import {
  subscribeForTest,
  homeStatusNow,
  noteRequestFailed,
  refreshHomeStatus,
  setHomeStatus,
  stripLines,
  stripSignature,
  type HomeStatus,
} from "./homeStatus";

const ok: HomeStatus = {
  v: 1,
  at: "2026-11-06T10:00:00Z",
  mode: "normal",
  overall: "ok",
  components: {
    database: "ok",
    api: "ok",
    streaming: "ok",
    processing: "ok",
    search: "ok",
    logs: "off",
  },
  features_down: [],
};
const viewer = { online: true, admin: false };
const admin = { online: true, admin: true };
const keys = (l: { key: string }[]) => l.map((x) => x.key);

afterEach(() => setHomeStatus(null));

describe("stripLines (the priority table)", () => {
  it("shows nothing when all is well, and nothing for parts that are off", () => {
    expect(stripLines(ok, viewer)).toEqual([]);
    expect(
      stripLines(
        { ...ok, components: { ...ok.components, streaming: "off", processing: "off" } },
        admin
      )
    ).toEqual([]);
    expect(stripLines(null, viewer)).toEqual([]);
  });

  it("puts offline first, then the home restarting, then safe mode, then a part", () => {
    const s: HomeStatus = {
      ...ok,
      mode: "safe",
      overall: "down",
      components: { ...ok.components, api: "restarting", streaming: "down" },
    };
    expect(keys(stripLines(s, { online: false, admin: false }))).toEqual([
      "offline",
      "restarting",
      "safe",
      "streaming",
    ]);
  });

  it("says the database when it is the database", () => {
    const s: HomeStatus = {
      ...ok,
      overall: "down",
      components: { ...ok.components, database: "down" },
    };
    const l = stripLines(s, admin);
    expect(l[0]).toEqual({ key: "database", text: "status.database", details: "/recovery" });
  });

  it("gives admins Details: the recovery page when the API is down, else System health", () => {
    const apiDown: HomeStatus = {
      ...ok,
      overall: "down",
      components: { ...ok.components, api: "down" },
    };
    expect(stripLines(apiDown, admin)[0].details).toBe("/recovery");
    expect(stripLines(apiDown, viewer)[0].details).toBeUndefined();
    const tvDown: HomeStatus = {
      ...ok,
      overall: "degraded",
      components: { ...ok.components, streaming: "degraded" },
    };
    expect(stripLines(tvDown, admin)[0].details).toBe("/admin/health");
  });

  it("shows processing to admins only (T03)", () => {
    const s: HomeStatus = {
      ...ok,
      overall: "degraded",
      components: { ...ok.components, processing: "degraded" },
    };
    expect(stripLines(s, viewer)).toEqual([]);
    expect(keys(stripLines(s, admin))).toEqual(["processing"]);
  });

  it("leaves Live TV out of the playback sentence when it is off (T03b)", () => {
    const s: HomeStatus = {
      ...ok,
      overall: "degraded",
      components: { ...ok.components, streaming: "down" },
    };
    expect(stripLines(s, { ...viewer, liveTvOff: true })[0].text).toBe("status.streamingNoTv");
    expect(stripLines(s, viewer)[0].text).toBe("status.streaming");
  });

  it("changes its signature when what it shows changes", () => {
    const a = stripLines({ ...ok, mode: "safe" }, viewer);
    const b = stripLines(
      { ...ok, mode: "safe", components: { ...ok.components, search: "down" } },
      viewer
    );
    expect(stripSignature(a)).not.toBe(stripSignature(b));
  });
});

describe("refreshHomeStatus (T02)", () => {
  it("keeps what it knew when the home does not answer or has no /status.json", async () => {
    setHomeStatus(ok);
    await refreshHomeStatus(
      vi.fn().mockResolvedValue(new Response("", { status: 404 })) as unknown as typeof fetch
    );
    expect(homeStatusNow()).toEqual(ok);
    await refreshHomeStatus(
      vi.fn().mockRejectedValue(new Error("down")) as unknown as typeof fetch
    );
    expect(homeStatusNow()).toEqual(ok);
    const doc = { ...ok, overall: "degraded" };
    await refreshHomeStatus(
      vi
        .fn()
        .mockResolvedValue(
          new Response(JSON.stringify(doc), { status: 200 })
        ) as unknown as typeof fetch
    );
    expect(homeStatusNow()?.overall).toBe("degraded");
  });

  it("reads at once on a server error, not on a component_off 409", () => {
    const spy = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 404 }));
    noteRequestFailed(503);
    expect(spy).not.toHaveBeenCalled(); // no strip mounted: nothing reads the status
    const stop = subscribeForTest();
    noteRequestFailed(409);
    expect(spy).not.toHaveBeenCalled();
    noteRequestFailed(503);
    expect(spy).toHaveBeenCalledTimes(1);
    stop();
    spy.mockRestore();
  });
});
