// Epic 57 (parental controls): the routes the web calls. The home decides every
// role; a page here only shows what the home answers and handles its refusals.
//
//   GET  /api/me/parental                          the caller's own limit
//   GET  /api/me/parental/time[?library_id=]       the caller's own time now
//   GET  /api/parental/children                    the restricted accounts the caller looks after
//   GET|PUT /api/parental/users/{id}/time-rules    admin, that child's guardian
//   GET  /api/parental/users/{id}/time-usage       admin, that child's guardian
//   POST /api/parental/users/{id}/time-grants      admin, that child's guardian
//   GET  /api/parental/review                      admin; a guardian reads
//   POST /api/parental/review/{video_id}           admin
//   PUT  /api/{videos,libraries,series,collections,channels}/{id}/age-tier,
//        /api/libraries/{id}/youtube/sources/{sid}/age-tier          admin
//   GET  /api/channels/{id}/access                 is the programme now within the limit
//   GET|PUT /api/tv/hdhr/allowed                   admin: who may use the tuner
//
// Every reader is defensive: a missing list is an empty one, a missing field its
// zero. A refusal is an ApiError the caller shows.
import { ApiError, api } from "./api";
import { problemSlug } from "./errorKind";

const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const str = (v: unknown, d = "") => (typeof v === "string" ? v : d);
const num = (v: unknown, d = 0) => (typeof v === "number" && Number.isFinite(v) ? v : d);
const numOrNull = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : null);
const strOrNull = (v: unknown) => (typeof v === "string" && v !== "" ? v : null);
const enc = encodeURIComponent;

// ---- tiers ----

/** The home's age tiers, youngest first. */
export const TIERS = [0, 9, 13, 16, 18] as const;
export type Tier = (typeof TIERS)[number];
/** A policy's limit when nothing with a tier is open. */
export const NOTHING_ALLOWED = -1;

/** The catalog key of a tier's label: null is unrated, -1 nothing unless allowed. */
export function tierKey(tier: number | null): string {
  if (tier === null) return "parental.tier.unrated";
  if (tier === NOTHING_ALLOWED) return "parental.tier.none";
  if (tier === 0) return "parental.tier.all";
  return `parental.tier.${tier}`;
}

// ---- the caller's own limit ----

export interface MyLimit {
  /** the account itself is restricted (not only this device) */
  restricted: boolean;
  /** the limit this sign-in is held to, account or device; null: none */
  maxTier: number | null;
  /** the limit this sign-in's device gave, when it gave one */
  deviceLimit: number | null;
  /** the device has since said something looser, which waits for a guardian or an admin */
  devicePending: boolean;
}

export async function fetchMyLimit(): Promise<MyLimit> {
  const r = obj(await api.get<unknown>("/api/me/parental"));
  return {
    restricted: r.restricted === true,
    maxTier: numOrNull(r.max_tier),
    deviceLimit: numOrNull(r.device_limit),
    devicePending: r.device_pending === true,
  };
}

export interface Child {
  id: string;
  username: string;
  maxTier: number | null;
}

/** The restricted accounts the caller may manage (an admin: all of them). */
export async function fetchChildren(): Promise<Child[]> {
  const r = obj(await api.get<unknown>("/api/parental/children"));
  return list(r.children)
    .map((c) => {
      const o = obj(c);
      return {
        id: str(o.id) || str(o.user_id),
        username: str(o.username),
        maxTier: numOrNull(o.max_tier),
      };
    })
    .filter((c) => c.id !== "");
}

// ---- time ----

export type TimeReason = "outside_slot" | "limit_reached" | "no_heartbeat" | "";

function timeReason(v: unknown): TimeReason {
  return v === "outside_slot" || v === "limit_reached" || v === "no_heartbeat" ? v : "";
}

/** GET /api/me/parental/time: what the home says about the caller's time now. */
export interface MyTime {
  allowed: boolean;
  /** a rule covers this moment, so remainingSeconds is a real countdown */
  limited: boolean;
  reason: TimeReason;
  remainingSeconds: number | null;
  slotEndsAt: string | null;
  nextAllowedAt: string | null;
}

export function readMyTime(raw: unknown): MyTime {
  const r = obj(raw);
  return {
    // only an explicit false refuses: the home refuses the stream itself anyway
    allowed: r.allowed !== false,
    limited: r.limited === true,
    reason: timeReason(r.reason),
    remainingSeconds: numOrNull(r.remaining_seconds),
    slotEndsAt: strOrNull(r.slot_ends_at),
    nextAllowedAt: strOrNull(r.next_allowed_at),
  };
}

export async function fetchMyTime(libraryId?: string): Promise<MyTime> {
  const qs = libraryId ? `?library_id=${enc(libraryId)}` : "";
  return readMyTime(await api.get<unknown>(`/api/me/parental/time${qs}`));
}

/** The 403 a stream or a progress post gets when the account's time is over. */
export const PROBLEM_PARENTAL_TIME = "https://maktaba.tv/problems/parental-time";

export interface TimeRefusal {
  reason: TimeReason;
  nextAllowedAt: string | null;
}

/** The time refusal in what a call threw, or null when it is something else. */
export function timeRefusal(e: unknown): TimeRefusal | null {
  if (!(e instanceof ApiError) || e.status !== 403) return null;
  const p = e.problem as unknown as Record<string, unknown>;
  if (problemSlug(e.problem.type) !== "parental-time" && p.code !== "parental_time") return null;
  return { reason: timeReason(p.reason), nextAllowedAt: strOrNull(p.next_allowed_at) };
}

/** Whether a call was refused because the account is restricted (403 `restricted`). */
export function isRestrictedRefusal(e: unknown): boolean {
  return e instanceof ApiError && e.status === 403 && e.problem.code === "restricted";
}

/** One time rule. `days` is a bitmask: bit 0 Sunday to bit 6 Saturday. */
export interface TimeRule {
  id?: string;
  /** "": the whole account */
  libraryId: string;
  days: number;
  /** "HH:MM", both or neither */
  slotStart: string;
  slotEnd: string;
  dailyMinutes: number | null;
}

export const ALL_DAYS = 127;
export const MAX_TIME_RULES = 50;
export const MAX_GRANT_MINUTES = 480;

/** The first choice the editor offers: one hour a day, 07:00 to 20:00, every day. */
export const FIRST_RULE: TimeRule = {
  libraryId: "",
  days: ALL_DAYS,
  slotStart: "07:00",
  slotEnd: "20:00",
  dailyMinutes: 60,
};

function toRule(v: unknown): TimeRule {
  const r = obj(v);
  return {
    ...(typeof r.id === "string" ? { id: r.id } : {}),
    libraryId: str(r.library_id),
    days: num(r.days, ALL_DAYS) & ALL_DAYS,
    // the home may answer "HH:MM:SS"
    slotStart: str(r.slot_start).slice(0, 5),
    slotEnd: str(r.slot_end).slice(0, 5),
    dailyMinutes: numOrNull(r.daily_minutes),
  };
}

function ruleBody(r: TimeRule): Record<string, unknown> {
  return {
    days: r.days,
    ...(r.libraryId ? { library_id: r.libraryId } : {}),
    ...(r.slotStart && r.slotEnd ? { slot_start: r.slotStart, slot_end: r.slotEnd } : {}),
    ...(r.dailyMinutes !== null ? { daily_minutes: r.dailyMinutes } : {}),
  };
}

export interface TimeRules {
  rules: TimeRule[];
  /** the home's time zone: every slot and day is in it */
  timeZone: string;
}

function toRules(v: unknown): TimeRules {
  const r = obj(v);
  return { rules: list(r.rules).map(toRule), timeZone: str(r.time_zone) };
}

const timePath = (userId: string, leaf: string) => `/api/parental/users/${enc(userId)}/${leaf}`;

export async function fetchTimeRules(userId: string): Promise<TimeRules> {
  return toRules(await api.get<unknown>(timePath(userId, "time-rules")));
}

/** Replaces every rule the account has (an empty list: no time limit). */
export async function saveTimeRules(userId: string, rules: TimeRule[]): Promise<TimeRules> {
  return toRules(
    await api.put<unknown>(timePath(userId, "time-rules"), { rules: rules.map(ruleBody) })
  );
}

export interface TimeGrant {
  id: string;
  day: string;
  minutes: number;
  usedSeconds: number;
}

export interface TimeUsage {
  today: string;
  timeZone: string;
  /** seconds watched per home day (YYYY-MM-DD), every library together */
  days: Array<{ day: string; seconds: number }>;
  grants: TimeGrant[];
}

function toGrant(v: unknown): TimeGrant {
  const g = obj(v);
  return {
    id: str(g.id),
    day: str(g.day),
    minutes: num(g.minutes),
    usedSeconds: num(g.used_seconds),
  };
}

/** The last seven home days of watching, and the extra time given. */
export async function fetchTimeUsage(userId: string): Promise<TimeUsage> {
  const r = obj(await api.get<unknown>(timePath(userId, "time-usage")));
  const byDay = new Map<string, number>();
  for (const u of list(r.usage)) {
    const o = obj(u);
    const day = str(o.day);
    if (day) byDay.set(day, (byDay.get(day) ?? 0) + num(o.seconds));
  }
  return {
    today: str(r.today),
    timeZone: str(r.time_zone),
    days: [...byDay.entries()]
      .map(([day, seconds]) => ({ day, seconds }))
      .sort((a, b) => (a.day < b.day ? 1 : -1)),
    grants: list(r.grants).map(toGrant),
  };
}

/** More minutes for the current home day only. */
export async function grantTime(userId: string, minutes: number): Promise<TimeGrant> {
  return toGrant(await api.post<unknown>(timePath(userId, "time-grants"), { minutes }));
}

// ---- age tiers an admin sets ----

export type TierTarget =
  | { kind: "video" | "library" | "series" | "collection" | "channel"; id: string }
  | { kind: "youtube_source"; libraryId: string; id: string };

export function tierPath(target: TierTarget): string {
  switch (target.kind) {
    case "video":
      return `/api/videos/${enc(target.id)}/age-tier`;
    case "library":
      return `/api/libraries/${enc(target.id)}/age-tier`;
    case "series":
      return `/api/series/${enc(target.id)}/age-tier`;
    case "collection":
      return `/api/collections/${enc(target.id)}/age-tier`;
    case "channel":
      return `/api/channels/${enc(target.id)}/age-tier`;
    case "youtube_source":
      return `/api/libraries/${enc(target.libraryId)}/youtube/sources/${enc(target.id)}/age-tier`;
  }
}

export interface TierAnswer {
  tier: number | null;
  source: string | null;
  /** how many videos the change rated again, or queued to be */
  recomputeCount: number;
}

function toTierAnswer(v: unknown): TierAnswer {
  const r = obj(v);
  return {
    tier: numOrNull(r.tier),
    source: strOrNull(r.source),
    recomputeCount: num(r.recompute_count),
  };
}

/** Sets (or, with null, clears) the admin's own tier. */
export async function setAgeTier(target: TierTarget, tier: Tier | null): Promise<TierAnswer> {
  return toTierAnswer(await api.put<unknown>(tierPath(target), { tier }));
}

// ---- the review list of detected tiers ----

export const REVIEW_STATES = ["low_confidence", "unconfirmed", "not_checked"] as const;
export type ReviewState = (typeof REVIEW_STATES)[number];

/** What the screen listens for, in the home's order. */
export const REVIEW_CATEGORIES = [
  "violence",
  "sexual_content",
  "coarse_language",
  "drugs_alcohol",
  "frightening_scenes",
  "mature_themes",
] as const;
export type ReviewCategory = (typeof REVIEW_CATEGORIES)[number];

export interface ReviewItem {
  videoId: string;
  title: string;
  libraryName: string;
  state: ReviewState;
  detectedTier: number | null;
  confidence: number;
  /** the categories heard (level 1 to 3), with up to three times in seconds */
  categories: Array<{ name: ReviewCategory; level: number; evidence: number[] }>;
  /** what the video is rated now, and from where */
  tier: number | null;
  source: string | null;
}

export interface ReviewList {
  items: ReviewItem[];
  total: number;
  minConfidence: number;
  limit: number;
  offset: number;
}

function toReviewItem(v: unknown): ReviewItem {
  const r = obj(v);
  const cats = obj(r.categories);
  const state = REVIEW_STATES.find((s) => s === r.state) ?? "low_confidence";
  return {
    videoId: str(r.video_id),
    title: str(r.title),
    libraryName: str(r.library_name),
    state,
    detectedTier: numOrNull(r.detected_tier),
    confidence: num(r.confidence),
    categories: REVIEW_CATEGORIES.flatMap((name) => {
      const c = obj(cats[name]);
      const level = num(c.level);
      if (level <= 0) return [];
      return [
        {
          name,
          level: Math.min(3, Math.round(level)),
          evidence: list(c.evidence).filter((x): x is number => typeof x === "number"),
        },
      ];
    }),
    tier: numOrNull(r.tier),
    source: strOrNull(r.source),
  };
}

export const REVIEW_PAGE = 50;

export async function fetchReview(state: ReviewState | "", offset = 0): Promise<ReviewList> {
  const q = new URLSearchParams({ limit: String(REVIEW_PAGE), offset: String(offset) });
  if (state) q.set("state", state);
  const r = obj(await api.get<unknown>(`/api/parental/review?${q.toString()}`));
  const items = list(r.items)
    .map(toReviewItem)
    .filter((i) => i.videoId !== "");
  return {
    items,
    total: num(r.total, items.length),
    minConfidence: num(r.min_confidence, 0.8),
    limit: num(r.limit, REVIEW_PAGE),
    offset: num(r.offset, offset),
  };
}

/** The admin's answer: the tier becomes the admin's own on the video. */
export async function decideReview(videoId: string, tier: Tier | null): Promise<TierAnswer> {
  return toTierAnswer(await api.post<unknown>(`/api/parental/review/${enc(videoId)}`, { tier }));
}

// ---- live TV ----

export interface ChannelAccess {
  restricted: boolean;
  allowed: boolean;
  /** when the answer may change (the programme's end) */
  until: string | null;
  /** the home's time for this answer; null from a home older than the field */
  now: string | null;
}

export async function fetchChannelAccess(channelId: string): Promise<ChannelAccess> {
  const r = obj(await api.get<unknown>(`/api/channels/${enc(channelId)}/access`));
  return {
    restricted: r.restricted === true,
    allowed: r.allowed !== false,
    until: strOrNull(r.until),
    now: strOrNull(r.now),
  };
}

/** The addresses (or CIDRs) allowed to use the tuner, beside this machine and Plex's own. */
export async function fetchTunerAllowed(): Promise<string[]> {
  const r = obj(await api.get<unknown>("/api/tv/hdhr/allowed"));
  return list(r.addresses).filter((a): a is string => typeof a === "string");
}

export async function saveTunerAllowed(addresses: string[]): Promise<string[]> {
  const r = obj(await api.put<unknown>("/api/tv/hdhr/allowed", { addresses }));
  return list(r.addresses).filter((a): a is string => typeof a === "string");
}

export const MAX_TUNER_ALLOWED = 64;
