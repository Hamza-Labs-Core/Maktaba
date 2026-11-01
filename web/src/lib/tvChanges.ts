// Typed client for TV changes (Epic 33, stories 33.10 / 33.11): what is live, what is
// staged, what exactly would change, whether it passed its checks, and the actions.
//
//   GET   /api/tv/changes?state=&limit=          {items, in_flight?, config, users}
//   GET   /api/tv/changes/{id}                   change + events + users + audit + release
//   PATCH /api/tv/changes/{id}                   {title?, swap_at?, swap_wait?}  (schedule)
//   POST  /api/tv/changes/{id}/stage|verify|promote|cancel
//   POST  /api/tv/changes/{id}/approve           {swap_at?, ack_param_change?}
//   GET   /api/tv/changes/{id}/schedule-diff?page= | ?channel=
//   GET   /api/tv/changes/{id}/boundaries?at=&swap_wait=
//   GET   /api/tv/rollback                       the rollback point (warm / cold / none)
//   POST  /api/tv/rollback                       {to_release_id?} → {mode, op_id?, rollback_change_id?, degraded?}
//   GET   /api/tv/slots, /api/tv/releases
//
// The page never re-implements the state machine: the buttons are the change's
// allowed_actions (plan 33.11 D2).
import { api } from "./api";

/** What a rollback would do now (story 33.9 AC7): warm (the previous slot is up: instant),
 * cold (the previous release is staged again: minutes), or none (and why). */
export type RollbackPoint = {
  mode: "warm" | "cold" | "none";
  change_id?: string;
  change_number?: number;
  release_id?: string;
  until?: string;
  slot?: string;
  reason?: string;
};

export type RollbackResult = {
  mode: "warm" | "cold";
  change_id: string;
  op_id?: number;
  rollback_change_id?: string;
  /** What a warm rollback could not do ("no_live_slot": the database only). */
  degraded?: string;
};

export type ChangeState =
  | "draft"
  | "staged"
  | "verifying"
  | "ready"
  | "approved"
  | "swapping"
  | "live"
  | "superseded"
  | "failed"
  | "cancelled"
  | "rolled_back";

export const CHANGE_STATES: ChangeState[] = [
  "draft",
  "staged",
  "verifying",
  "ready",
  "approved",
  "swapping",
  "live",
  "superseded",
  "failed",
  "cancelled",
  "rolled_back",
];

export type ChangeAction =
  | "stage"
  | "verify"
  | "approve"
  | "schedule"
  | "promote"
  | "cancel"
  | "rollback";

/** The order the action buttons are always laid out in (fixed: no reflow on a poll). */
export const ACTION_ORDER: ChangeAction[] = [
  "stage",
  "verify",
  "approve",
  "schedule",
  "promote",
  "cancel",
  "rollback",
];

export type CheckResult = "pass" | "warn" | "fail" | "skip";

export interface VerifyCheck {
  check: string;
  channel_id?: string;
  result: CheckResult;
  /** The home's English; `code` names it and `params` fill it (tvchanges.value.<code>). */
  value?: string;
  code?: string;
  params?: Record<string, string>;
}

export interface VerifyReport {
  checks?: VerifyCheck[];
  passed?: boolean;
  warnings?: number;
  at?: string;
  interim?: boolean;
  error?: string;
  /** story 33.5: "slots" when measured on the staging slot */
  runtime?: string;
  slot?: string;
  /** warming / soaking while verifying */
  status?: string;
  soak_started_at?: string;
  /** why the change failed */
  reasons?: string[];
  summary?: { pass: number; warn: number; fail: number };
  budget?: { staging: number; capacity: number; live: number; standby: number };
  /** what the swap commit reported (tv_swap) */
  swap?: Record<string, unknown>;
}

export interface ChannelRef {
  id: string;
  number?: number | null;
  name?: string | null;
}

export interface LineupDiff {
  added: ChannelRef[];
  removed: ChannelRef[];
  renumbered: { id: string; from: number; to: number }[];
  renamed: { id: string; from: string; to: string }[];
  changed: (ChannelRef & { fields: string[] })[];
}

export interface ScheduleEntry extends ChannelRef {
  reason: "new" | "regenerated";
  divergence_at?: string | null;
}

export interface SettingChange {
  key: string;
  from: unknown;
  to: unknown;
}

export interface ArtChange extends ChannelRef {
  what: ("logo_path" | "look" | "overrides")[];
}

export interface ChangeDiff {
  lineup?: LineupDiff;
  schedules?: ScheduleEntry[];
  settings?: Record<string, SettingChange[]>;
  art?: ArtChange[];
  binary?: { from: string; to: string } | null;
  pending?: Record<string, unknown>;
  stage?: { requested_at?: string; enqueued_at?: string };
}

export interface ChangeEvent {
  id: number;
  at: string;
  actor_user_id?: string;
  actor_source: string;
  from_state?: string;
  to_state?: string;
  action: string;
  detail: Record<string, unknown>;
}

export interface AuditRow {
  id: number;
  at: string;
  action: string;
  actor_user_id?: string;
  actor_source: string;
  payload: Record<string, unknown>;
}

export interface PendingSummary {
  channels: { id: string; number?: number; name?: string; op: string }[];
  settings: string[];
  /** the filler pools the draft adds, edits, removes or changes the clips of */
  filler?: {
    id: string;
    name: string;
    op: string;
    items_added: number;
    items_removed: number;
  }[];
}

export interface Release {
  id: string;
  digest: string;
  kinds: string[];
  sched_gen: number;
  streaming_version: string;
  base_release_id?: string;
  created_at: string;
  live: boolean;
  changes: string[];
}

export interface TvChange {
  id: string;
  number: number;
  release_id?: string;
  base_release_id?: string;
  state: ChangeState;
  kinds: string[];
  title: string;
  source: { kind?: string; job_id?: string; [k: string]: unknown };
  digest?: string;
  diff: ChangeDiff;
  verify_report: VerifyReport;
  auto_rule?: string;
  /** Who made it (slot 0169): an automatic change goes on by its rule, a manual one waits
   * for an admin's approval. */
  origin?: "auto" | "manual";
  approved_by: string[];
  approved_at?: string;
  anchor_at?: string;
  swap_at?: string;
  swap_wait?: "boundary" | "immediate";
  created_by?: string;
  created_at: string;
  updated_at: string;
  staged_at?: string;
  ready_at?: string;
  swapped_at?: string;
  ended_at?: string;
  end_reason?: string;
  allowed_actions: ChangeAction[];
  events?: ChangeEvent[];
}

export interface TvChangeDetail extends TvChange {
  users: Record<string, string>;
  audit: AuditRow[];
  pending_summary?: PendingSummary;
  release?: Release;
}

export interface StagingConfig {
  enabled: boolean;
  require_second_admin: boolean;
  swap_wait: "boundary" | "immediate";
  boundary_max_wait_minutes: number;
  quiet_window: string;
  quiet_max_viewers: number;
  auto_promote: Record<string, string>;
  keep_warm_minutes: number;
  rollback_retention_hours: number;
  param_change_push: string;
}

export interface ChangeList {
  items: TvChange[];
  in_flight?: string;
  config: StagingConfig;
  users: Record<string, string>;
}

export interface TvSlot {
  host_id: string;
  slot: string;
  role: string;
  state: string;
  release_id?: string;
  version?: string;
}

export interface SlotsView {
  runtime: "single" | "slots";
  slots: TvSlot[];
}

export interface Programme {
  start_at: string;
  end_at: string;
  kind: string;
  title: string;
  video_id?: string;
}

export interface SchedulePage {
  channels: ScheduleEntry[];
  page: number;
  pages: number;
  total: number;
}

export interface ScheduleChannelDiff {
  channel: ScheduleEntry;
  from: string;
  to: string;
  before: Programme[];
  after: Programme[];
  staged: boolean;
  unchanged: boolean;
}

export interface Boundary {
  channel_id: string;
  number: number;
  name: string;
  switch_at: string;
  on_air?: string;
  capped?: boolean;
}

export interface Boundaries {
  at: string;
  swap_wait: "boundary" | "immediate";
  boundary_max_wait_minutes: number;
  channels: Boundary[];
  last_switch_at?: string | null;
}

const base = (id: string) => `/api/tv/changes/${encodeURIComponent(id)}`;

export const tvChangesApi = {
  list: (states?: ChangeState[], limit = 100) => {
    const q = new URLSearchParams({ limit: String(limit) });
    if (states && states.length) q.set("state", states.join(","));
    return api.get<ChangeList>(`/api/tv/changes?${q}`);
  },
  get: (id: string) => api.get<TvChangeDetail>(base(id)),
  stage: (id: string) => api.post<TvChangeDetail>(`${base(id)}/stage`),
  verify: (id: string) => api.post<TvChangeDetail>(`${base(id)}/verify`),
  approve: (id: string, body: { swap_at?: string | null; ack_param_change?: boolean }) =>
    api.post<TvChangeDetail>(`${base(id)}/approve`, body),
  schedule: (id: string, body: { swap_at: string | null; swap_wait?: "boundary" | "immediate" }) =>
    api.patch<TvChangeDetail>(base(id), body),
  promote: (id: string) => api.post<TvChangeDetail>(`${base(id)}/promote`),
  cancel: (id: string) => api.post<TvChangeDetail>(`${base(id)}/cancel`),
  rollback: () => api.post<RollbackResult>(`/api/tv/rollback`, {}),
  rollbackPoint: () => api.get<RollbackPoint>(`/api/tv/rollback`),
  preview: (id: string) => api.post<Preview>(`${base(id)}/preview`),
  slots: () => api.get<SlotsView>(`/api/tv/slots`),
  releases: (limit = 20) => api.get<{ items: Release[] }>(`/api/tv/releases?limit=${limit}`),
  scheduleChannels: (id: string, page = 1) =>
    api.get<SchedulePage>(`${base(id)}/schedule-diff?page=${page}`),
  scheduleChannel: (id: string, channel: string) =>
    api.get<ScheduleChannelDiff>(
      `${base(id)}/schedule-diff?channel=${encodeURIComponent(channel)}`
    ),
  boundaries: (id: string, at?: string, swapWait?: "boundary" | "immediate") => {
    const q = new URLSearchParams();
    if (at) q.set("at", at);
    if (swapWait) q.set("swap_wait", swapWait);
    const qs = q.toString();
    return api.get<Boundaries>(`${base(id)}/boundaries${qs ? `?${qs}` : ""}`);
  },
};

/** One channel of a staging preview (story 33.6). */
export interface PreviewChannel {
  id: string;
  number: number;
  name: string;
  logo?: boolean;
  changed?: boolean;
  added?: boolean;
  removed?: boolean;
  /** the staging slot's HLS (web origin, with the token) */
  url: string;
  /** the live channel, for "live vs staging" (absent for an added channel) */
  live_url?: string;
}

/** POST /api/tv/changes/{id}/preview: signed staging URLs for an hour. */
export interface Preview {
  hls_base: string;
  token: string;
  m3u: string;
  xmltv: string;
  expires_at: string;
  channels: PreviewChannel[];
}

/** Whether a state is one a change is "in flight" in (README R9.4). */
export const IN_FLIGHT: ReadonlySet<ChangeState> = new Set([
  "staged",
  "verifying",
  "ready",
  "approved",
  "swapping",
]);

/** The checks summary of a report: pass / warn / fail counts (skip left out). */
export function checksSummary(r: VerifyReport | undefined): {
  pass: number;
  warn: number;
  fail: number;
  total: number;
} {
  const out = { pass: 0, warn: 0, fail: 0, total: 0 };
  for (const c of r?.checks ?? []) {
    if (c.result === "skip") continue;
    out[c.result] += 1;
    out.total += 1;
  }
  return out;
}

/** Whether the approval needs the parameter-change acknowledgement (push.continuity). */
export function needsParamAck(c: TvChange): boolean {
  return (c.verify_report?.checks ?? []).some(
    (x) => x.check === "push.continuity" && x.result === "fail"
  );
}

/**
 * Keeps the previous object when nothing in it changed, so a poll that brings the same
 * data re-renders nothing (PR #76's rule: in-place updates, no flicker).
 */
export function reconcile<T>(prev: T | null | undefined, next: T): T {
  if (prev == null) return next;
  return JSON.stringify(prev) === JSON.stringify(next) ? prev : next;
}

/** A list reconciled item by item (by id): unchanged items keep their objects. */
export function reconcileList<T extends { id: string }>(
  prev: T[] | null | undefined,
  next: T[]
): T[] {
  if (!prev) return next;
  const old = new Map(prev.map((x) => [x.id, x]));
  let same = prev.length === next.length;
  const out = next.map((x, i) => {
    const kept = reconcile(old.get(x.id), x);
    if (kept !== prev[i]) same = false;
    return kept;
  });
  return same ? prev : out;
}
