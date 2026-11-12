// Epic 57 family settings: the web's readers and writers of the home's routes.
// Every reader is defensive (a missing list is empty, a missing field its zero),
// and every writer sends exactly what the home's handler takes.
import { describe, it, expect, vi, beforeEach } from "vitest";

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
}));
vi.mock("./api", async () => {
  const actual = await vi.importActual<typeof import("./api")>("./api");
  return { ...actual, api: { ...actual.api, get, post, put, delete: del } };
});

import { ApiError } from "./api";
import {
  addGuardian,
  addRule,
  approveRequest,
  askFor,
  askForTime,
  clearSignal,
  denyRequest,
  fetchAccounts,
  fetchActivity,
  fetchPinSet,
  fetchPolicy,
  fetchRequests,
  fetchRules,
  fetchUnlockers,
  liftRestriction,
  refusalCode,
  removeGuardian,
  removeRule,
  retryAfter,
  savePolicy,
  setPin,
  unlockWithPin,
  validPin,
} from "./familyApi";

const problem = (status: number, extra: Record<string, unknown> = {}, retry?: number) =>
  new ApiError({ type: "about:blank", title: "refused", status, ...extra } as never, retry);

beforeEach(() => {
  for (const f of [get, post, put, del]) f.mockReset();
});

describe("the limit", () => {
  it("reads the policy, who may do what, the guardians and the device signals", async () => {
    get.mockResolvedValue({
      policy: { user_id: "c1", max_tier: 13, unrated: "tier_9", allow_only: true, version: 4 },
      access: "admin",
      guardians: [{ id: "g1", username: "mum" }, { username: "no id" }],
      signals: [
        {
          refresh_family_id: "f1",
          lower: 9,
          upper: 12,
          tier_limit: 9,
          pending_looser: { tier_limit: 13, observed_at: "2026-11-10T10:00:00Z" },
          observed_at: "2026-11-01T10:00:00Z",
        },
        { refresh_family_id: "f2", observed_at: "2026-11-02T10:00:00Z" },
      ],
    });
    const p = await fetchPolicy("c1");
    expect(get).toHaveBeenCalledWith("/api/users/c1/parental");
    expect(p).toMatchObject({
      userId: "c1",
      maxTier: 13,
      unrated: "tier_9",
      allowOnly: true,
      version: 4,
      access: "admin",
      guardians: [{ id: "g1", username: "mum" }],
    });
    expect(p?.signals[0]).toMatchObject({
      family: "f1",
      lower: 9,
      upper: 12,
      tierLimit: 9,
      pending: { tierLimit: 13 },
    });
    expect(p?.signals[1]).toMatchObject({ family: "f2", tierLimit: null, pending: null });
  });

  it("answers null for the home's 404, and throws anything else", async () => {
    get.mockRejectedValueOnce(problem(404));
    expect(await fetchPolicy("c1")).toBeNull();
    get.mockRejectedValueOnce(problem(500));
    await expect(fetchPolicy("c1")).rejects.toBeInstanceOf(ApiError);
  });

  it("an unknown unrated rule reads as hide, an unknown access as guardian", async () => {
    get.mockResolvedValue({ policy: { max_tier: 0, unrated: "tier_99" }, access: "owner" });
    expect(await fetchPolicy("c1")).toMatchObject({ unrated: "hide", access: "guardian" });
  });

  it("saves with the version that was read; version 0 makes the account restricted", async () => {
    put.mockResolvedValue({ policy: { user_id: "c1", max_tier: -1, version: 1 }, access: "admin" });
    const p = await savePolicy("c 1", {
      maxTier: -1,
      unrated: "hide",
      allowOnly: false,
      version: 0,
    });
    expect(put).toHaveBeenCalledWith("/api/users/c%201/parental", {
      max_tier: -1,
      unrated: "hide",
      allow_only: false,
      version: 0,
    });
    expect(p.maxTier).toBe(-1);
  });

  it("lifts, names and removes guardians, and lists the home's accounts", async () => {
    await liftRestriction("c1");
    expect(del).toHaveBeenCalledWith("/api/users/c1/parental");
    post.mockResolvedValue({ guardians: [{ id: "g1", username: "mum" }] });
    expect(await addGuardian("c1", "g1")).toEqual([{ id: "g1", username: "mum" }]);
    expect(post).toHaveBeenCalledWith("/api/users/c1/parental/guardians", { guardian_id: "g1" });
    await removeGuardian("c1", "g1");
    expect(del).toHaveBeenCalledWith("/api/users/c1/parental/guardians/g1");
    get.mockResolvedValue({ users: [{ id: "u1", username: "sara" }, null], libraries: [] });
    expect(await fetchAccounts()).toEqual([{ id: "u1", username: "sara" }]);
    expect(get).toHaveBeenCalledWith("/api/admin/library-acl");
  });
});

describe("rules", () => {
  it("reads the rules, dropping what it cannot name", async () => {
    get.mockResolvedValue({
      rules: [
        { id: "r1", effect: "allow", subject_kind: "series", subject_id: "s1", created_at: "x" },
        { id: "r2", effect: "block", subject_kind: "video", subject_id: "v1", expires_at: "y" },
        { id: "r3", effect: "allow", subject_kind: "spaceship", subject_id: "z" },
      ],
    });
    const rules = await fetchRules("c1");
    expect(rules.map((r) => [r.id, r.effect, r.subjectKind, r.expiresAt])).toEqual([
      ["r1", "allow", "series", null],
      ["r2", "block", "video", "y"],
    ]);
  });

  it("adds with an expiry only when there is one, and removes by id", async () => {
    post.mockResolvedValue({
      id: "r9",
      effect: "allow",
      subject_kind: "library",
      subject_id: "l1",
    });
    await addRule("c1", {
      effect: "allow",
      subjectKind: "library",
      subjectId: "l1",
      expiresAt: null,
    });
    expect(post).toHaveBeenLastCalledWith("/api/users/c1/parental/rules", {
      effect: "allow",
      subject_kind: "library",
      subject_id: "l1",
    });
    await addRule("c1", {
      effect: "block",
      subjectKind: "library",
      subjectId: "l1",
      expiresAt: "2026-11-12T20:59:59.000Z",
    });
    expect(post).toHaveBeenLastCalledWith("/api/users/c1/parental/rules", {
      effect: "block",
      subject_kind: "library",
      subject_id: "l1",
      expires_at: "2026-11-12T20:59:59.000Z",
    });
    // until the home's day ends: the home computes the instant
    await addRule("c1", {
      effect: "allow",
      subjectKind: "library",
      subjectId: "l1",
      until: "day_end",
    });
    expect(post).toHaveBeenLastCalledWith("/api/users/c1/parental/rules", {
      effect: "allow",
      subject_kind: "library",
      subject_id: "l1",
      until: "day_end",
    });
    await removeRule("c1", "r9");
    expect(del).toHaveBeenCalledWith("/api/users/c1/parental/rules/r9");
  });
});

describe("devices", () => {
  it("answers a sign-in's signal", async () => {
    post.mockResolvedValue({ tier_limit: 13, removed: false });
    expect(await clearSignal("c1", "fam/1")).toEqual({ tierLimit: 13, removed: false });
    expect(post).toHaveBeenCalledWith("/api/users/c1/parental/signals/fam%2F1/clear");
    post.mockResolvedValue({ tier_limit: null, removed: true });
    expect(await clearSignal("c1", "f")).toEqual({ tierLimit: null, removed: true });
  });
});

describe("requests", () => {
  it("lists the waiting requests with the child and the subject", async () => {
    get.mockResolvedValue({
      requests: [
        {
          id: "q1",
          child_id: "c1",
          subject_kind: "video",
          subject_id: "v1",
          status: "pending",
          created_at: "2026-11-11T10:00:00Z",
          child: { id: "c1", username: "sara" },
          subject: { kind: "video", id: "v1", title: "Space", tier: 13, poster_url: "/p" },
        },
        {
          id: "q2",
          child_id: "c1",
          subject_kind: "time",
          subject_id: "30",
          minutes: 30,
          status: "pending",
          child: { id: "c1", username: "sara" },
          subject: { kind: "time", id: "30" },
        },
      ],
    });
    const list = await fetchRequests();
    expect(get).toHaveBeenCalledWith("/api/parental/requests?status=pending");
    expect(list[0]).toMatchObject({
      id: "q1",
      child: { username: "sara" },
      subject: { title: "Space", tier: 13, posterUrl: "/p", hidden: false },
    });
    expect(list[1]).toMatchObject({ subjectKind: "time", minutes: 30 });
  });

  it("approves one thing with an expiry, time with minutes, and denies", async () => {
    post.mockResolvedValue({ request: { id: "q1", status: "approved" } });
    await approveRequest("q1", { expiresAt: "2026-11-12T20:59:59.000Z" });
    expect(post).toHaveBeenLastCalledWith("/api/parental/requests/q1/approve", {
      expires_at: "2026-11-12T20:59:59.000Z",
    });
    await approveRequest("q1", { until: "day_end" });
    expect(post).toHaveBeenLastCalledWith("/api/parental/requests/q1/approve", {
      until: "day_end",
    });
    await approveRequest("q2", { minutes: 45 });
    expect(post).toHaveBeenLastCalledWith("/api/parental/requests/q2/approve", { minutes: 45 });
    await approveRequest("q3");
    expect(post).toHaveBeenLastCalledWith("/api/parental/requests/q3/approve", {});
    post.mockResolvedValue({ request: { id: "q1", status: "denied" } });
    expect((await denyRequest("q1")).status).toBe("denied");
    expect(post).toHaveBeenLastCalledWith("/api/parental/requests/q1/deny");
  });
});

describe("activity", () => {
  it("reads a page: a time event has minutes and no subject", async () => {
    get.mockResolvedValue({
      events: [
        { id: "e1", kind: "time_granted", at: "2026-11-11T10:00:00Z", minutes: 30 },
        {
          id: "e2",
          kind: "approved",
          at: "2026-11-11T09:00:00Z",
          subject: { kind: "video", id: "v1", title: "Space" },
          guardian: { id: "g1", username: "mum" },
        },
        { id: "e3", kind: "signal", at: "2026-11-11T08:00:00Z", device: "ios" },
        { id: "e4", kind: "from the future", at: "2026-11-11T07:00:00Z" },
      ],
      next_cursor: "abc",
    });
    const page = await fetchActivity("c1", "approved", "cur");
    expect(get).toHaveBeenCalledWith("/api/users/c1/parental/activity?kind=approved&cursor=cur");
    expect(page.nextCursor).toBe("abc");
    expect(page.events[0]).toMatchObject({ kind: "time_granted", minutes: 30, subject: null });
    expect(page.events[1]).toMatchObject({
      subject: { title: "Space" },
      guardian: { username: "mum" },
    });
    expect(page.events[2].device).toBe("ios");
    expect(page.events[3].kind).toBe("");
  });

  it("asks for the first page with no query", async () => {
    get.mockResolvedValue({});
    expect(await fetchActivity("c1")).toEqual({ events: [], nextCursor: null });
    expect(get).toHaveBeenCalledWith("/api/users/c1/parental/activity");
  });
});

describe("the parent PIN", () => {
  it("reads whether one is set, and sets it with the password", async () => {
    get.mockResolvedValue({ set: true });
    expect(await fetchPinSet()).toBe(true);
    await setPin("123456", "pw");
    expect(put).toHaveBeenCalledWith("/api/me/parental-pin", { pin: "123456", password: "pw" });
  });

  it("takes six digits only", () => {
    expect(validPin("123456")).toBe(true);
    for (const bad of ["12345", "1234567", "12a456", "١٢٣٤٥٦", ""])
      expect(validPin(bad)).toBe(false);
  });
});

describe("a restricted account", () => {
  it("asks for one thing, and for more time with the minutes as the id", async () => {
    post.mockResolvedValue({ request: { id: "q1", status: "pending", subject_kind: "video" } });
    expect((await askFor("video", "v1")).id).toBe("q1");
    expect(post).toHaveBeenLastCalledWith("/api/me/parental/requests", {
      subject_kind: "video",
      subject_id: "v1",
    });
    await askForTime(30);
    expect(post).toHaveBeenLastCalledWith("/api/me/parental/requests", {
      subject_kind: "time",
      subject_id: "30",
    });
  });

  it("lists who can type a PIN", async () => {
    get.mockResolvedValue({ guardians: [{ id: "g1", username: "mum" }] });
    expect(await fetchUnlockers()).toEqual([{ id: "g1", username: "mum" }]);
    expect(get).toHaveBeenCalledWith("/api/me/parental/guardians");
  });

  it("unlocks one thing (a rule) or more time (a grant)", async () => {
    post.mockResolvedValue({ rule: { id: "r1", expires_at: "2026-11-12T01:00:00Z" } });
    expect(await unlockWithPin("g1", "123456", "video", "v1")).toEqual({
      expiresAt: "2026-11-12T01:00:00Z",
      minutes: null,
    });
    expect(post).toHaveBeenLastCalledWith("/api/me/parental/unlock", {
      guardian_id: "g1",
      pin: "123456",
      subject_kind: "video",
      subject_id: "v1",
    });
    post.mockResolvedValue({ grant: { id: "t1", minutes: 30 } });
    expect(await unlockWithPin("g1", "123456", "time", "30")).toEqual({
      expiresAt: null,
      minutes: 30,
    });
  });
});

describe("refusals", () => {
  it("reads the home's code and how long to wait", () => {
    expect(refusalCode(problem(403, { code: "pin_wrong" }))).toBe("pin_wrong");
    expect(refusalCode(new Error("x"))).toBe("");
    expect(retryAfter(problem(429, { retry_after: 900 }))).toBe(900);
    expect(retryAfter(problem(429, {}, 60))).toBe(60);
    expect(retryAfter(problem(403, { retry_after: 900 }))).toBeNull();
  });
});
