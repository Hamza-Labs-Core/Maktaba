// Epic 57: the parental routes as the web reads and writes them.
import { describe, it, expect, vi, beforeEach } from "vitest";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { ApiError } from "./api";
import {
  decideReview,
  fetchChannelAccess,
  fetchChildren,
  fetchMyLimit,
  fetchMyTime,
  fetchReview,
  fetchTimeRules,
  fetchTimeUsage,
  grantTime,
  isRestrictedRefusal,
  readMyTime,
  saveTimeRules,
  saveTunerAllowed,
  setAgeTier,
  tierKey,
  tierPath,
  timeRefusal,
} from "./parentalApi";

beforeEach(() => {
  for (const f of [get, post, put]) f.mockReset();
});

describe("tiers", () => {
  it("names each tier, unrated and nothing-unless-allowed", () => {
    expect(tierKey(0)).toBe("parental.tier.all");
    expect(tierKey(13)).toBe("parental.tier.13");
    expect(tierKey(null)).toBe("parental.tier.unrated");
    expect(tierKey(-1)).toBe("parental.tier.none");
  });

  it("addresses each thing an admin rates", () => {
    expect(tierPath({ kind: "video", id: "v 1" })).toBe("/api/videos/v%201/age-tier");
    expect(tierPath({ kind: "library", id: "l1" })).toBe("/api/libraries/l1/age-tier");
    expect(tierPath({ kind: "series", id: "s1" })).toBe("/api/series/s1/age-tier");
    expect(tierPath({ kind: "collection", id: "c1" })).toBe("/api/collections/c1/age-tier");
    expect(tierPath({ kind: "channel", id: "ch1" })).toBe("/api/channels/ch1/age-tier");
    expect(tierPath({ kind: "youtube_source", libraryId: "l1", id: "y1" })).toBe(
      "/api/libraries/l1/youtube/sources/y1/age-tier"
    );
  });

  it("sets and clears a tier, and reads the home's answer", async () => {
    put.mockResolvedValue({ tier: 13, source: "admin_container", recompute_count: 42 });
    expect(await setAgeTier({ kind: "series", id: "s1" }, 13)).toEqual({
      tier: 13,
      source: "admin_container",
      recomputeCount: 42,
    });
    expect(put).toHaveBeenCalledWith("/api/series/s1/age-tier", { tier: 13 });
    put.mockResolvedValue({ tier: null, source: null, recompute_count: 1 });
    expect((await setAgeTier({ kind: "video", id: "v1" }, null)).tier).toBeNull();
    expect(put).toHaveBeenLastCalledWith("/api/videos/v1/age-tier", { tier: null });
  });
});

describe("the caller's own limit", () => {
  it("reads a restricted account and one limited only by its device", async () => {
    get.mockResolvedValue({ restricted: true, max_tier: 9, unrated: "hide", version: 3 });
    expect(await fetchMyLimit()).toEqual({ restricted: true, maxTier: 9 });
    get.mockResolvedValue({ restricted: false, max_tier: 13, device_limit: 13 });
    expect(await fetchMyLimit()).toEqual({ restricted: false, maxTier: 13 });
    get.mockResolvedValue({ restricted: false });
    expect(await fetchMyLimit()).toEqual({ restricted: false, maxTier: null });
  });

  it("lists the accounts the caller looks after; a broken answer is none", async () => {
    get.mockResolvedValue({
      children: [{ id: "u1", username: "sara", user_id: "u1", max_tier: 13 }, { username: "x" }],
    });
    expect(await fetchChildren()).toEqual([{ id: "u1", username: "sara", maxTier: 13 }]);
    get.mockResolvedValue(null);
    expect(await fetchChildren()).toEqual([]);
  });
});

describe("time", () => {
  it("reads the home's answer about the caller's time", async () => {
    get.mockResolvedValue({
      allowed: true,
      limited: true,
      remaining_seconds: 540,
      slot_ends_at: "2026-11-11T17:00:00Z",
      next_allowed_at: null,
      now: "2026-11-11T16:51:00Z",
    });
    expect(await fetchMyTime("lib-1")).toEqual({
      allowed: true,
      limited: true,
      reason: "",
      remainingSeconds: 540,
      slotEndsAt: "2026-11-11T17:00:00Z",
      nextAllowedAt: null,
    });
    expect(get).toHaveBeenCalledWith("/api/me/parental/time?library_id=lib-1");
  });

  it("only an explicit false refuses", () => {
    expect(readMyTime({}).allowed).toBe(true);
    expect(readMyTime(null).allowed).toBe(true);
    const over = readMyTime({ allowed: false, reason: "limit_reached", remaining_seconds: 0 });
    expect(over.allowed).toBe(false);
    expect(over.reason).toBe("limit_reached");
    expect(readMyTime({ allowed: false, reason: "something new" }).reason).toBe("");
  });

  it("knows the 403 parental-time refusal by its type or its code, with its reason", () => {
    const byType = new ApiError({
      type: "https://maktaba.tv/problems/parental-time",
      title: "time is over",
      status: 403,
      reason: "outside_slot",
      next_allowed_at: "2026-11-12T04:00:00Z",
    } as never);
    expect(timeRefusal(byType)).toEqual({
      reason: "outside_slot",
      nextAllowedAt: "2026-11-12T04:00:00Z",
    });
    const byCode = new ApiError({
      type: "about:blank",
      title: "x",
      status: 403,
      code: "parental_time",
      reason: "limit_reached",
    } as never);
    expect(timeRefusal(byCode)).toEqual({ reason: "limit_reached", nextAllowedAt: null });
    // any other refusal is not a time refusal
    expect(
      timeRefusal(new ApiError({ type: "about:blank", title: "forbidden", status: 403 }))
    ).toBeNull();
    expect(timeRefusal(new Error("boom"))).toBeNull();
  });

  it("knows the 403 a restricted account gets for what it may not do", () => {
    const e = new ApiError({ type: "about:blank", title: "no", status: 403, code: "restricted" });
    expect(isRestrictedRefusal(e)).toBe(true);
    expect(isRestrictedRefusal(new ApiError({ type: "", title: "no", status: 403 }))).toBe(false);
  });

  it("reads rules (seconds dropped from a slot) and writes only what a rule has", async () => {
    get.mockResolvedValue({
      time_zone: "Asia/Riyadh",
      rules: [
        { id: "r1", days: 127, slot_start: "07:00:00", slot_end: "20:00:00", daily_minutes: 60 },
        { id: "r2", days: 65, library_id: "lib-1", daily_minutes: 30 },
      ],
    });
    const got = await fetchTimeRules("u 1");
    expect(get).toHaveBeenCalledWith("/api/parental/users/u%201/time-rules");
    expect(got.timeZone).toBe("Asia/Riyadh");
    expect(got.rules[0]).toMatchObject({ slotStart: "07:00", slotEnd: "20:00", dailyMinutes: 60 });
    expect(got.rules[1]).toMatchObject({ libraryId: "lib-1", days: 65, slotStart: "" });

    put.mockResolvedValue({ rules: [], time_zone: "Asia/Riyadh" });
    await saveTimeRules("u1", [
      { libraryId: "", days: 127, slotStart: "07:00", slotEnd: "20:00", dailyMinutes: null },
      { libraryId: "lib-1", days: 2, slotStart: "", slotEnd: "", dailyMinutes: 45 },
    ]);
    expect(put).toHaveBeenCalledWith("/api/parental/users/u1/time-rules", {
      rules: [
        { days: 127, slot_start: "07:00", slot_end: "20:00" },
        { days: 2, library_id: "lib-1", daily_minutes: 45 },
      ],
    });
  });

  it("adds a day's libraries together and gives more time", async () => {
    get.mockResolvedValue({
      today: "2026-11-11",
      time_zone: "UTC",
      usage: [
        { day: "2026-11-10", library_id: "a", seconds: 600 },
        { day: "2026-11-11", library_id: "a", seconds: 1200 },
        { day: "2026-11-11", library_id: "b", seconds: 300 },
      ],
      grants: [{ id: "g1", day: "2026-11-11", minutes: 30, used_seconds: 0 }],
    });
    const u = await fetchTimeUsage("u1");
    expect(u.days).toEqual([
      { day: "2026-11-11", seconds: 1500 },
      { day: "2026-11-10", seconds: 600 },
    ]);
    expect(u.grants[0]).toMatchObject({ minutes: 30, day: "2026-11-11" });
    post.mockResolvedValue({ id: "g2", day: "2026-11-11", minutes: 15, used_seconds: 0 });
    expect((await grantTime("u1", 15)).minutes).toBe(15);
    expect(post).toHaveBeenCalledWith("/api/parental/users/u1/time-grants", { minutes: 15 });
  });
});

describe("the review list", () => {
  it("reads items, keeping only what was heard, and sends the admin's answer", async () => {
    get.mockResolvedValue({
      total: 1,
      min_confidence: 0.8,
      limit: 50,
      offset: 0,
      items: [
        {
          video_id: "v1",
          title: "A talk",
          library_name: "Lectures",
          state: "low_confidence",
          detected_tier: 13,
          confidence: 0.55,
          categories: {
            violence: { level: 2, evidence: [12.5, 300] },
            coarse_language: { level: 0, evidence: [] },
          },
          tier: null,
          source: null,
        },
        { title: "no id" },
      ],
    });
    const r = await fetchReview("low_confidence", 50);
    expect(get).toHaveBeenCalledWith(
      "/api/parental/review?limit=50&offset=50&state=low_confidence"
    );
    expect(r.items).toHaveLength(1);
    expect(r.items[0]!.categories).toEqual([{ name: "violence", level: 2, evidence: [12.5, 300] }]);
    post.mockResolvedValue({ tier: 13, source: "admin_video", recompute_count: 1 });
    expect((await decideReview("v1", 13)).source).toBe("admin_video");
    expect(post).toHaveBeenCalledWith("/api/parental/review/v1", { tier: 13 });
  });
});

describe("live TV", () => {
  it("reads whether the programme now may be watched", async () => {
    get.mockResolvedValue({
      channel_id: "c1",
      restricted: true,
      allowed: false,
      until: "2026-11-11T20:00:00Z",
    });
    expect(await fetchChannelAccess("c1")).toEqual({
      restricted: true,
      allowed: false,
      until: "2026-11-11T20:00:00Z",
    });
    expect(get).toHaveBeenCalledWith("/api/channels/c1/access");
  });

  it("stores the tuner's allowed addresses", async () => {
    put.mockResolvedValue({ addresses: ["192.168.1.20", "10.0.0.0/8"] });
    expect(await saveTunerAllowed(["192.168.1.20", "10.0.0.0/8"])).toEqual([
      "192.168.1.20",
      "10.0.0.0/8",
    ]);
    expect(put).toHaveBeenCalledWith("/api/tv/hdhr/allowed", {
      addresses: ["192.168.1.20", "10.0.0.0/8"],
    });
  });
});
