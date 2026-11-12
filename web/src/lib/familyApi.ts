// Epic 57 (parental controls): family settings on the web. The home decides every
// role; a page only shows what the home answers and handles its refusals.
//
//   GET|PUT|DELETE /api/users/{id}/parental               the limit; lifting is the admin's
//   POST   /api/users/{id}/parental/guardians             admin
//   DELETE /api/users/{id}/parental/guardians/{gid}       admin
//   GET|POST /api/users/{id}/parental/rules               admin, that child's guardian
//   DELETE /api/users/{id}/parental/rules/{rid}           admin, that child's guardian
//   POST   /api/users/{id}/parental/signals/{family}/clear   admin, that child's guardian
//   GET    /api/users/{id}/parental/activity?kind=&cursor=   admin, that child's guardian
//   GET    /api/parental/requests?status=                 admin, a guardian
//   POST   /api/parental/requests/{id}/approve|deny       admin, that child's guardian
//   GET|PUT /api/me/parental-pin                          admin, a guardian (their own PIN)
//   POST   /api/me/parental/requests                      a restricted account
//   GET    /api/me/parental/guardians                     a restricted account
//   POST   /api/me/parental/unlock                        a restricted account
//   GET    /api/admin/library-acl                         admin: the home's accounts, to pick from
//
// Every reader is defensive: a missing list is an empty one, a missing field its
// zero. A refusal is an ApiError the caller shows.
import { ApiError, api } from "./api";

const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const str = (v: unknown, d = "") => (typeof v === "string" ? v : d);
const num = (v: unknown, d = 0) => (typeof v === "number" && Number.isFinite(v) ? v : d);
const numOrNull = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : null);
const strOrNull = (v: unknown) => (typeof v === "string" && v !== "" ? v : null);
const enc = encodeURIComponent;
const userPath = (userId: string, leaf = "") =>
  `/api/users/${enc(userId)}/parental${leaf ? `/${leaf}` : ""}`;

/** The short `code` of the home's refusal ("stale", "pin_wrong", ...), or "". */
export function refusalCode(e: unknown): string {
  return e instanceof ApiError ? str(e.problem.code) : "";
}

// ---- the limit ----

/** What unrated content is to an account: hidden, or treated as a tier. */
export const UNRATED = ["hide", "tier_0", "tier_9", "tier_13", "tier_16", "tier_18"] as const;
export type Unrated = (typeof UNRATED)[number];

/** The limits a policy may have, loosest choice last; -1: nothing unless allowed. */
export const POLICY_LIMITS = [0, 9, 13, 16, 18, -1] as const;

export interface Person {
  id: string;
  username: string;
}

export interface DeviceSignal {
  /** the sign-in the device spoke for */
  family: string;
  lower: number | null;
  upper: number | null;
  /** how the device knows ("self_declared", "guardian_declared", ...); "" when it did not say */
  declaration: string;
  movieCeiling: number | null;
  tvCeiling: number | null;
  /** the limit in force on that sign-in; null: none */
  tierLimit: number | null;
  /** a looser statement that waits for an answer */
  pending: { tierLimit: number | null; observedAt: string } | null;
  observedAt: string;
}

export interface ChildPolicy {
  userId: string;
  maxTier: number;
  unrated: Unrated;
  allowOnly: boolean;
  /** sent back with a change: the home refuses a change made on an older one */
  version: number;
  /** what the caller may do: an admin also lifts and names guardians */
  access: "admin" | "guardian";
  guardians: Person[];
  signals: DeviceSignal[];
}

function toPerson(v: unknown): Person {
  const o = obj(v);
  return { id: str(o.id), username: str(o.username) };
}

function toSignal(v: unknown): DeviceSignal {
  const o = obj(v);
  const p = o.pending_looser && typeof o.pending_looser === "object" ? obj(o.pending_looser) : null;
  return {
    family: str(o.refresh_family_id),
    lower: numOrNull(o.lower),
    upper: numOrNull(o.upper),
    declaration: str(o.declaration),
    movieCeiling: numOrNull(o.movie_ceiling),
    tvCeiling: numOrNull(o.tv_ceiling),
    tierLimit: numOrNull(o.tier_limit),
    pending: p ? { tierLimit: numOrNull(p.tier_limit), observedAt: str(p.observed_at) } : null,
    observedAt: str(o.observed_at),
  };
}

function toPolicy(v: unknown, userId: string): ChildPolicy {
  const r = obj(v);
  const p = obj(r.policy);
  const unrated = UNRATED.find((u) => u === p.unrated) ?? "hide";
  return {
    userId: str(p.user_id) || userId,
    maxTier: num(p.max_tier),
    unrated,
    allowOnly: p.allow_only === true,
    version: num(p.version),
    access: r.access === "admin" ? "admin" : "guardian",
    guardians: list(r.guardians)
      .map(toPerson)
      .filter((g) => g.id !== ""),
    signals: list(r.signals)
      .map(toSignal)
      .filter((s) => s.family !== ""),
  };
}

/**
 * The account's limit, or null when the home answers 404: the account is not
 * restricted, or it is not one the caller looks after (the home does not say which).
 */
export async function fetchPolicy(userId: string): Promise<ChildPolicy | null> {
  try {
    return toPolicy(await api.get<unknown>(userPath(userId)), userId);
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return null;
    throw e;
  }
}

export interface PolicyInput {
  maxTier: number;
  unrated: Unrated;
  allowOnly: boolean;
  /** 0: make the account restricted (admin); otherwise the version that was read */
  version: number;
}

export async function savePolicy(userId: string, input: PolicyInput): Promise<ChildPolicy> {
  return toPolicy(
    await api.put<unknown>(userPath(userId), {
      max_tier: input.maxTier,
      unrated: input.unrated,
      allow_only: input.allowOnly,
      version: input.version,
    }),
    userId
  );
}

/** Lifts the restriction: its rules, requests, activity and time rules go with it. */
export async function liftRestriction(userId: string): Promise<void> {
  await api.delete(userPath(userId));
}

export async function addGuardian(userId: string, guardianId: string): Promise<Person[]> {
  const r = obj(
    await api.post<unknown>(userPath(userId, "guardians"), { guardian_id: guardianId })
  );
  return list(r.guardians)
    .map(toPerson)
    .filter((g) => g.id !== "");
}

export async function removeGuardian(userId: string, guardianId: string): Promise<void> {
  await api.delete(userPath(userId, `guardians/${enc(guardianId)}`));
}

/** The home's accounts, for an admin to pick from (the home refuses anyone else). */
export async function fetchAccounts(): Promise<Person[]> {
  const r = obj(await api.get<unknown>("/api/admin/library-acl"));
  return list(r.users)
    .map(toPerson)
    .filter((u) => u.id !== "");
}

// ---- allow and block rules ----

export const SUBJECT_KINDS = [
  "library",
  "collection",
  "series",
  "channel",
  "youtube_source",
  "plugin",
  "video",
] as const;
export type SubjectKind = (typeof SUBJECT_KINDS)[number];
export type RuleEffect = "allow" | "block";

const subjectKind = (v: unknown): SubjectKind | null => SUBJECT_KINDS.find((k) => k === v) ?? null;

export interface Rule {
  id: string;
  effect: RuleEffect;
  subjectKind: SubjectKind;
  subjectId: string;
  expiresAt: string | null;
  createdAt: string;
}

function toRule(v: unknown): Rule | null {
  const o = obj(v);
  const kind = subjectKind(o.subject_kind);
  const id = str(o.id);
  if (!kind || !id) return null;
  return {
    id,
    effect: o.effect === "block" ? "block" : "allow",
    subjectKind: kind,
    subjectId: str(o.subject_id),
    expiresAt: strOrNull(o.expires_at),
    createdAt: str(o.created_at),
  };
}

export async function fetchRules(userId: string): Promise<Rule[]> {
  const r = obj(await api.get<unknown>(userPath(userId, "rules")));
  return list(r.rules)
    .map(toRule)
    .filter((x): x is Rule => x !== null);
}

export interface RuleInput {
  effect: RuleEffect;
  subjectKind: SubjectKind;
  subjectId: string;
  /** an instant (ISO 8601); null: the rule does not end */
  expiresAt: string | null;
}

export async function addRule(userId: string, input: RuleInput): Promise<Rule> {
  const made = toRule(
    await api.post<unknown>(userPath(userId, "rules"), {
      effect: input.effect,
      subject_kind: input.subjectKind,
      subject_id: input.subjectId,
      ...(input.expiresAt ? { expires_at: input.expiresAt } : {}),
    })
  );
  if (!made) throw new Error("malformed rule");
  return made;
}

export async function removeRule(userId: string, ruleId: string): Promise<void> {
  await api.delete(userPath(userId, `rules/${enc(ruleId)}`));
}

// ---- device signals ----

export interface ClearedSignal {
  /** the device's limit now; null: none */
  tierLimit: number | null;
  /** nothing waited, so the signal itself was removed */
  removed: boolean;
}

/**
 * The answer to a sign-in's signal: a looser statement that waited is applied;
 * with none waiting the signal is removed.
 */
export async function clearSignal(userId: string, family: string): Promise<ClearedSignal> {
  const r = obj(await api.post<unknown>(userPath(userId, `signals/${enc(family)}/clear`)));
  return { tierLimit: numOrNull(r.tier_limit), removed: r.removed === true };
}

// ---- what was asked for ----

/** What a request, an event or a rule is about, as the home lets this reader see it. */
export interface Subject {
  /** a rule's kinds, `time`, or an event's own (guardian, account, source, device) */
  kind: string;
  id: string;
  title: string;
  posterUrl: string | null;
  tier: number | null;
  /** deleted since */
  removed: boolean;
  /** in a library this reader cannot open: no title, poster or tier */
  hidden: boolean;
}

function toSubject(v: unknown): Subject {
  const o = obj(v);
  return {
    kind: str(o.kind),
    id: str(o.id),
    title: str(o.title),
    posterUrl: strOrNull(o.poster_url),
    tier: numOrNull(o.tier),
    removed: o.removed === true,
    hidden: o.hidden === true,
  };
}

export const REQUEST_STATUSES = ["pending", "approved", "denied", "expired"] as const;
export type RequestStatus = (typeof REQUEST_STATUSES)[number];

export interface AskRequest {
  id: string;
  childId: string;
  /** a rule's kinds, or `time` (more minutes today) */
  subjectKind: string;
  subjectId: string;
  /** the minutes a `time` request asks for; 0 otherwise */
  minutes: number;
  status: RequestStatus;
  createdAt: string;
  expiresAt: string;
}

function toRequest(v: unknown): AskRequest {
  const o = obj(v);
  return {
    id: str(o.id),
    childId: str(o.child_id),
    subjectKind: str(o.subject_kind),
    subjectId: str(o.subject_id),
    minutes: num(o.minutes),
    status: REQUEST_STATUSES.find((s) => s === o.status) ?? "pending",
    createdAt: str(o.created_at),
    expiresAt: str(o.expires_at),
  };
}

export interface RequestItem extends AskRequest {
  child: Person;
  subject: Subject;
}

/** The requests of the children the caller looks after (an admin: of every child). */
export async function fetchRequests(status: RequestStatus = "pending"): Promise<RequestItem[]> {
  const r = obj(await api.get<unknown>(`/api/parental/requests?status=${enc(status)}`));
  return list(r.requests)
    .map((v) => {
      const o = obj(v);
      return { ...toRequest(o), child: toPerson(o.child), subject: toSubject(o.subject) };
    })
    .filter((x) => x.id !== "");
}

export interface Approval {
  /** when the approval ends (content only); left out: it does not end */
  expiresAt?: string;
  /** the minutes to give (a `time` request only); left out: the minutes asked for */
  minutes?: number;
}

export async function approveRequest(id: string, approval: Approval = {}): Promise<AskRequest> {
  const r = obj(
    await api.post<unknown>(`/api/parental/requests/${enc(id)}/approve`, {
      ...(approval.expiresAt ? { expires_at: approval.expiresAt } : {}),
      ...(approval.minutes !== undefined ? { minutes: approval.minutes } : {}),
    })
  );
  return toRequest(r.request);
}

export async function denyRequest(id: string): Promise<AskRequest> {
  const r = obj(await api.post<unknown>(`/api/parental/requests/${enc(id)}/deny`));
  return toRequest(r.request);
}

// ---- activity ----

export const EVENT_KINDS = [
  "watched",
  "blocked",
  "asked",
  "approved",
  "denied",
  "pin_locked",
  "signal",
  "time_up",
  "time_granted",
] as const;
export type EventKind = (typeof EVENT_KINDS)[number];

export interface ActivityEvent {
  id: string;
  kind: EventKind | "";
  at: string;
  /** absent for an event with none (the time events) */
  subject: Subject | null;
  guardian: Person | null;
  /** the device of a `signal` event */
  device: string;
  /** a time event's minutes: asked for, given, or watched that day */
  minutes: number | null;
}

export interface ActivityPage {
  events: ActivityEvent[];
  /** the next page's cursor; null on the last */
  nextCursor: string | null;
}

/** A child's events, newest first, fifty a page. */
export async function fetchActivity(
  userId: string,
  kind: EventKind | "" = "",
  cursor = ""
): Promise<ActivityPage> {
  const q = new URLSearchParams();
  if (kind) q.set("kind", kind);
  if (cursor) q.set("cursor", cursor);
  const qs = q.toString();
  const r = obj(await api.get<unknown>(userPath(userId, "activity") + (qs ? `?${qs}` : "")));
  return {
    events: list(r.events)
      .map((v): ActivityEvent => {
        const o = obj(v);
        return {
          id: str(o.id),
          kind: EVENT_KINDS.find((k) => k === o.kind) ?? "",
          at: str(o.at),
          subject: o.subject && typeof o.subject === "object" ? toSubject(o.subject) : null,
          guardian: o.guardian && typeof o.guardian === "object" ? toPerson(o.guardian) : null,
          device: str(o.device),
          minutes: numOrNull(o.minutes),
        };
      })
      .filter((e) => e.id !== ""),
    nextCursor: strOrNull(r.next_cursor),
  };
}

// ---- the parent PIN ----

/** Whether the caller has set a parent PIN. The PIN itself is never sent back. */
export async function fetchPinSet(): Promise<boolean> {
  return obj(await api.get<unknown>("/api/me/parental-pin")).set === true;
}

/** Six digits, as the home takes them. */
export const validPin = (pin: string) => /^[0-9]{6}$/.test(pin);

/** Sets (or changes) the caller's own PIN; the home asks for the account password again. */
export async function setPin(pin: string, password: string): Promise<void> {
  await api.put("/api/me/parental-pin", { pin, password });
}

// ---- a restricted account: ask, and unlock with a parent's PIN ----

/** The minutes a child may ask for at one tap. */
export const ASK_MINUTES = [15, 30, 60] as const;

async function ask(subjectKind: string, subjectId: string): Promise<AskRequest> {
  // 201 is a new request, 200 the one already waiting; both carry it
  const r = obj(
    await api.post<unknown>("/api/me/parental/requests", {
      subject_kind: subjectKind,
      subject_id: subjectId,
    })
  );
  return toRequest(r.request);
}

/** Asks the guardians for one thing the limit hides. */
export const askFor = (kind: SubjectKind, id: string) => ask(kind, id);

/** Asks the guardians for more minutes today. */
export const askForTime = (minutes: number) => ask("time", String(minutes));

/** Who can type a PIN for the caller: their guardians and the admins with a PIN set. */
export async function fetchUnlockers(): Promise<Person[]> {
  const r = obj(await api.get<unknown>("/api/me/parental/guardians"));
  return list(r.guardians)
    .map(toPerson)
    .filter((g) => g.id !== "");
}

/**
 * A guardian's PIN, typed on the child's screen. For one thing the limit hides it
 * adds an allow rule until the home's day ends (`expiresAt`); for `time` (the id
 * is the minutes, as text) it gives that many minutes today (`minutes`).
 */
export async function unlockWithPin(
  guardianId: string,
  pin: string,
  kind: SubjectKind | "time",
  id: string
): Promise<{ expiresAt: string | null; minutes: number | null }> {
  const r = obj(
    await api.post<unknown>("/api/me/parental/unlock", {
      guardian_id: guardianId,
      pin,
      subject_kind: kind,
      subject_id: id,
    })
  );
  return {
    expiresAt: strOrNull(obj(r.rule).expires_at),
    minutes: r.grant ? numOrNull(obj(r.grant).minutes) : null,
  };
}

/** The seconds the home asks to wait after too many wrong tries (429), or null. */
export function retryAfter(e: unknown): number | null {
  if (!(e instanceof ApiError) || e.status !== 429) return null;
  const p = e.problem as unknown as Record<string, unknown>;
  return numOrNull(p.retry_after) ?? e.retryAfter ?? null;
}
