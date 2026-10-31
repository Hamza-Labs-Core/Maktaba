// The home's automatic updates (Epic 35, story 35.3), admin only:
//
//   GET  /api/cluster/updates            policy, the next planned update, the catalog
//   PUT  /api/cluster/updates/settings   rules.updates keys (400 pin_below_installed, bad_window, …)
//   POST /api/cluster/updates/run        {version?, confirm:true}  Update now -> 202 {run_id}
//   POST /api/cluster/updates/skip       {version}
//   POST /api/cluster/updates/defer      {until}
//   POST /api/cluster/updates/check      check GitHub now
//   GET  /api/cluster/updates/runs[/{id}] history, a run with its steps and machines (35.4, 35.12)
//   POST /api/cluster/updates/runs/{id}/cancel|resume, /targets/{node}/retry|skip|rollback
//   GET  /api/cluster/updates/backups    the home's backups and kept releases (35.5 AC8)
//   GET  /api/cluster/updates/backups/{name}/restore   what restoring it does
//   POST /api/cluster/updates/backups/{name}/restore   {confirm: "<name>"} -> 202 (the home restarts)
import { api } from "./api";
import type { HomeDb } from "./homeDb";

export interface UpdatesPolicy {
  auto_install: boolean;
  channel: "stable" | "beta";
  window: string;
  window_overrun_minutes: number;
  min_release_age_hours: number;
  pinned_version: string;
  deferred_until: string | null;
  skipped_versions: string[];
  node_failure_policy: string;
  keep_versions: number;
}

export interface CatalogRelease {
  version: string;
  tag: string;
  channel: string;
  published_at?: string;
  release_url?: string;
  notes?: string;
  verify_state: "unverified" | "verified" | "failed";
  verify_error?: { kind: string; file?: string; detail?: string };
  withdrawn_at?: string;
  held_at?: string;
  approved_at?: string;
  /** why automatic runs no longer install it (attempts, rolled_back, verify, rehearsal): an
   * admin tries it again (unblock) or installs it now */
  blocked_reason?: string;
  attempts?: number;
  manifest?: {
    breaks_previous_home?: boolean;
    min_upgrade_from?: string;
    /** the library database's schema the release runs (newer than the home's: the update
     * migrates the database, and backs it up first) */
    schema_rev?: number;
  };
}

/** The scheduler's last decision: why a due release does not start now. */
export interface UpdatesTick {
  action: "none" | "held" | "blocked" | "withdrawn" | "started" | "failed";
  reason?: string;
  version?: string;
  run_id?: string;
  at: string;
}

/** A release a gate (the TV watched, the window's end) held for several windows in a row. */
export interface UpdatesStuck {
  version: string;
  code: string;
  windows: number;
  since: string;
}

export interface UpdatesPlan {
  target?: CatalogRelease;
  newest?: CatalogRelease;
  held: boolean;
  stepping_stone: boolean;
  reason?: string;
  auto_install: boolean;
  due: boolean;
  next_run_at?: string;
  eligible_at?: string;
  waiting?: string;
}

/** One machine of the Updates page: the home, then every joined machine (story 35.12). */
export interface UpdateMachine {
  id: string;
  name: string;
  role: "home" | "node";
  version: string;
  /** up_to_date, behind, updating (the home during a run), ahead_of_home, offline, worker_only,
   * or a run's target state */
  state: string;
  platform?: string;
  /** the install kind: layout, runtime, appbundle, flat */
  kind?: string;
  /** the release the active run brings it to (the one its desktop app downloads and installs) */
  target?: string;
  /** its last update attempts, newest first (at most 5): see UpdateAttempt */
  attempts?: UpdateAttempt[];
  /** a node: whether it reaches the home's database (its machine agent's report) */
  home_db?: HomeDb;
  /** a worker-only machine (story 35.8 AC8): next_update (its next update makes it a full
   * node) or installer (its pipeline is too old for that: re-run the installer once) */
  convert?: "next_update" | "installer";
  /** its last update that completed (never a rolled-back, failed or cancelled try) */
  last_update?: UpdateAttempt;
  /** a machine behind the cluster's release: what happens next (the catch-up's decision) */
  next?: UpdateNext;
}

/** What happens next to a machine behind the cluster's release (api updates.NextStep). */
export interface UpdateNext {
  version: string;
  /** catch_up_now, catch_up_window (at: the window's start), catch_up_retry (at), held_back,
   * busy, offline, auto_install_off, not_verified */
  reason: string;
  at?: string;
}

/** One try at bringing a machine to a release, whoever made it (update-attempts.md): the
 * desktop app's own updater (its badge, its menu, the home's order), a release's first start
 * on the machine (its database step, its pipeline environment), or a run of the cluster. */
export interface UpdateAttempt {
  id: string;
  /** when it began, and when it ended (absent while it runs) */
  at: string;
  ended_at?: string;
  source: "app" | "server" | "run";
  /** badge, menu, order (app); start (server); a run's trigger (run) */
  trigger?: string;
  from?: string;
  to: string;
  /** running, installed, failed, put_back (the release failed to start; the database and the
   * release before are back), rolled_back, cancelled */
  outcome: string;
  /** the step it is at, or stopped at (updates.step.<step>) */
  step?: string;
  /** the reason (updates.code.<code>) */
  code?: string;
  /** the updater's or the server's own words (English) */
  detail?: string;
  /** the machine's own suggestion (English): the page shows its own words for the reason's
   * fix (updates.fix.<code>), and this only as what the machine said, never to copy */
  fix?: string;
  /** a run's attempt: the run */
  run_id?: string;
}

/** The outcomes of an attempt that did not bring the machine to its release. */
export const ATTEMPT_FAILED = new Set(["failed", "put_back", "rolled_back"]);

/** An update run (update_runs). */
export interface UpdateRun {
  id: string;
  version: string;
  from_version: string;
  trigger: "auto" | "manual" | "catch_up" | "retry";
  state: string;
  step: string;
  started_at: string;
  finished_at?: string;
  error?: { code?: string; step?: string; detail?: string; reason?: string; [k: string]: unknown };
}

export interface UpdateRunStep {
  seq: number;
  host: string;
  step: string;
  state: string;
  started_at: string;
  finished_at?: string;
  detail?: { detail?: string };
}

export interface UpdateTarget {
  node_id: string;
  name: string;
  ord: number;
  platform: string;
  kind: string;
  /** pending, preparing, prepared, ordered, app_staged, applying, healthy, failed_prepare, failed,
   * rolled_back, held, skipped, offline, worker_only, needs_person (an install that cannot update
   * itself as it is: its error's detail says what to do) */
  state: string;
  from_version: string;
  to_version: string;
  attempts: number;
  /** code (updates.code.<code>), detail: the server's words, which can carry the fix */
  error?: { code?: string; detail?: string; held_by?: string; [k: string]: unknown };
  applied_at?: string;
  healthy_at?: string;
}

/** A failed check of a TV change's report (its channel and value). */
export interface TVCheck {
  check: string;
  channel_id?: string;
  result: string;
  value?: string;
}

/** What a TV held at an update is now: held (Retry or Drop), retrying (checked again),
 * live (a retry passed and swapped), dropped, replaced (a later release's change), failed
 * (a retry's swap failed), gone. */
export type RunTVState =
  | "held"
  | "retrying"
  | "live"
  | "dropped"
  | "replaced"
  | "failed"
  | "gone"
  | "pending";

/** A TV held at the home's commit (the owner's decision, 2026-10-28): its change's check
 * failed before any swap, so the release installed and the live TV stayed on `held_on`. */
export interface RunTV {
  outcome: "held";
  change_id?: string;
  number?: number;
  held_on?: string;
  failed?: string[];
  state?: RunTVState;
  checks?: TVCheck[];
}

/** A machine that waits for the home to commit the run's release before its order. */
export interface WaitingMachine {
  node_id: string;
  name: string;
  version?: string;
}

/** GET /api/cluster/updates/runs/{id}. */
export interface UpdateRunView extends UpdateRun {
  install_kind: string;
  detail: {
    waiting?: string;
    no_home?: boolean;
    home?: { state?: string };
    tv?: RunTV;
    /** The machines that wait for the home to commit before their order (none ordered when it does not). */
    waiting_home?: WaitingMachine[];
  };
  steps: UpdateRunStep[];
  targets: UpdateTarget[];
}

/** The run whose TV is held now (GET /api/cluster/updates: tv_held). */
export interface TVHeldNow extends RunTV {
  run_id: string;
  version: string;
}

/** A held TV that Retry and Drop act on (one checked again waits for its check). */
export function tvHeld(tv?: RunTV | null): boolean {
  return !!tv && (tv.state ?? "held") === "held";
}

export interface UpdatesOverview {
  current_version: string;
  watermark: string;
  policy: UpdatesPolicy;
  auto_install_available: boolean;
  plan: UpdatesPlan;
  run?: UpdateRun;
  last_run?: UpdateRun;
  catalog: CatalogRelease[];
  last_check_ok?: string;
  last_check_error?: string;
  check_stale: boolean;
  time_zone: string;
  machines?: UpdateMachine[];
  last_tick?: UpdatesTick;
  stuck?: UpdatesStuck;
  tv_held?: TVHeldNow;
}

/** The states of a run that are over. */
export const RUN_ENDED = new Set([
  "done",
  "done_with_failures",
  "done_needs_person",
  "held",
  "failed",
  "rolled_back",
  "failed_rollback",
  "cancelled",
  "superseded",
]);

/** A run may be cancelled until the home begins switching (the API answers 409 past_switch after), and
 * during its rollout (the machines not yet updating are left as they are). */
export const RUN_CANCELLABLE = new Set([
  "planned",
  "home_preparing",
  "home_prepared",
  "draining",
  "home_committed",
  "rolling_out",
]);

/** The states of a desktop app that runs the server, ordered the release before its go: its app
 * downloads it (ordered), then it waits for its turn (app_staged). It installs and restarts by
 * itself, with no click there (owner decision D1, 2026-10-27); the click states are those two as
 * older runs name them. */
export const APP_ORDERED = new Set(["ordered", "waiting_click", "awaiting_click"]);
export const APP_STAGED = new Set(["app_staged", "clicked"]);
export const APP_ORDER = new Set([...APP_ORDERED, ...APP_STAGED]);

/** A machine of a run that is over may be retried in these states (a new run for it): it failed,
 * was held or offline, or needs a person (an install that cannot update itself as it is: the
 * fix is in its error's detail). */
export const TARGET_RETRYABLE = new Set([
  "failed",
  "failed_prepare",
  "rolled_back",
  "held",
  "offline",
  "needs_person",
]);

/** A machine of a run under way may be skipped in these states: not yet updating (a desktop app's
 * order before its go is withdrawn), or needing a person. */
export const TARGET_SKIPPABLE = new Set([
  "pending",
  "preparing",
  "prepared",
  "held",
  ...APP_ORDER,
  "needs_person",
]);

/** Whether a run may be cancelled now: before the home switches (its desktop app's order is withdrawn
 * until its go), or during the rollout. */
export function runCancellable(view: Pick<UpdateRunView, "state" | "detail">): boolean {
  if (RUN_CANCELLABLE.has(view.state)) return true;
  return view.state === "home_applying" && APP_ORDER.has(view.detail?.home?.state ?? "");
}

/** One dump in the home's backups folder. */
export interface HomeBackup {
  name: string;
  bytes: number;
  at: string;
  /** update (before an update's migrations: from is the release whose database it holds),
   * failed (what a failed update left), replaced (what a restore replaced), daily */
  kind: "update" | "failed" | "replaced" | "daily";
  from?: string;
  to?: string;
}

export interface HomeBackups {
  backups: HomeBackup[];
  kept: { version: string; schema?: number; running?: boolean }[];
  running: string;
  /** a restore asked and not finished: the home's next start does it */
  restoring?: string;
}

/** What restoring a backup does: the home restarts on `release` (the kept release of the
 * dump's schema); every change since `at` is lost. */
export interface RestorePlan {
  backup: string;
  schema: number;
  release: string;
  running: string;
  at: string;
}

export const updatesApi = {
  overview: () => api.get<UpdatesOverview>("/api/cluster/updates"),
  saveSettings: (set: Partial<Record<keyof UpdatesPolicy, unknown>>) =>
    api.put<{ section: string; changed: string[] }>("/api/cluster/updates/settings", set),
  runNow: (version?: string) =>
    api.post<{ run_id: string; version: string }>("/api/cluster/updates/run", {
      ...(version ? { version } : {}),
      confirm: true,
    }),
  skip: (version: string) => api.post("/api/cluster/updates/skip", { version }),
  unblock: (version: string) =>
    api.post<{ unblocked: boolean; version: string }>("/api/cluster/updates/unblock", { version }),
  defer: (until: string) => api.post("/api/cluster/updates/defer", { until }),
  check: () => api.post("/api/cluster/updates/check", {}),
  runs: (limit = 20) => api.get<{ items: UpdateRun[] }>(`/api/cluster/updates/runs?limit=${limit}`),
  run: (id: string) =>
    api.get<UpdateRunView>(`/api/cluster/updates/runs/${encodeURIComponent(id)}`),
  cancel: (id: string) =>
    api.post(`/api/cluster/updates/runs/${encodeURIComponent(id)}/cancel`, {}),
  resume: (id: string) =>
    api.post<{ run_id: string }>(`/api/cluster/updates/runs/${encodeURIComponent(id)}/resume`, {}),
  targetAction: (id: string, node: string, action: "retry" | "skip" | "rollback") =>
    api.post(
      `/api/cluster/updates/runs/${encodeURIComponent(id)}/targets/${encodeURIComponent(node)}/${action}`,
      {}
    ),
  /** A TV held at the update: check its change again (retry), or withdraw it (drop). */
  tvAction: (id: string, action: "retry" | "drop") =>
    api.post<RunTV>(`/api/cluster/updates/runs/${encodeURIComponent(id)}/tv/${action}`, {}),
  logURL: (id: string) => `/api/cluster/updates/runs/${encodeURIComponent(id)}/log`,
  backups: () => api.get<HomeBackups>("/api/cluster/updates/backups"),
  restorePlan: (name: string) =>
    api.get<RestorePlan>(`/api/cluster/updates/backups/${encodeURIComponent(name)}/restore`),
  restore: (name: string) =>
    api.post<RestorePlan>(`/api/cluster/updates/backups/${encodeURIComponent(name)}/restore`, {
      confirm: name,
    }),
};

/** A quiet window "HH:MM-HH:MM" (may cross midnight), longer than 30 minutes. */
export function validWindow(s: string): boolean {
  const m = /^\s*(\d{1,2}):(\d{2})\s*-\s*(\d{1,2}):(\d{2})\s*$/.exec(s);
  if (!m) return false;
  const [h1, m1, h2, m2] = m.slice(1).map(Number) as [number, number, number, number];
  if (h1 > 23 || h2 > 23 || m1 > 59 || m2 > 59) return false;
  const len = (h2 * 60 + m2 - (h1 * 60 + m1) + 1440) % 1440;
  return len > 30;
}

const VERSION_RE = /^v?(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?$/;

/** A release version ("1.5.0", "v1.5.0-rc.1"). */
export function validVersion(s: string): boolean {
  return VERSION_RE.test(s.trim());
}

/** A `git describe` build once normalized: a tag, the commits after it, the commit. */
const DESCRIBE_RE = /^(\d+\.\d+\.\d+(?:-[0-9a-z.]+)?)-(\d+)-g[0-9a-f]{4,40}$/;

/** A version's release tag (no "v", no build metadata), its commits after the tag and
 * whether its tree was dirty: 0.2.0-12-gabc1234 is 12 commits after 0.2.0 (the server's
 * releaseverify.splitDescribe). */
function splitDescribe(v: string): { tag: string; commits: number; dirty: boolean } {
  let s = v.trim().toLowerCase().replace(/^v/, "");
  const plus = s.indexOf("+");
  if (plus >= 0) s = s.slice(0, plus);
  let dirty = false;
  if (s.endsWith("-dirty")) {
    dirty = true;
    s = s.slice(0, -"-dirty".length);
  }
  const m = DESCRIBE_RE.exec(s);
  return m ? { tag: m[1] ?? s, commits: Number(m[2]), dirty } : { tag: s, commits: 0, dirty };
}

const sign = (n: number): number => (n > 0 ? 1 : n < 0 ? -1 : 0);

/** Two release tags: the numeric core, then a prerelease below its release, prereleases by
 * their dot-separated identifiers (numeric ones numerically, below words). */
function compareReleases(a: string, b: string): number {
  const [ac = "", ...ap] = a.split("-");
  const [bc = "", ...bp] = b.split("-");
  const an = ac.split(".").map(Number);
  const bn = bc.split(".").map(Number);
  for (let i = 0; i < Math.max(an.length, bn.length); i++) {
    const d = (an[i] ?? 0) - (bn[i] ?? 0);
    if (d !== 0) return sign(d);
  }
  const apre = ap.join("-");
  const bpre = bp.join("-");
  if (apre === "" || bpre === "") return apre === bpre ? 0 : apre === "" ? 1 : -1;
  const as = apre.split(".");
  const bs = bpre.split(".");
  for (let i = 0; i < Math.min(as.length, bs.length); i++) {
    const x = as[i] ?? "";
    const y = bs[i] ?? "";
    const xn = /^\d+$/.test(x);
    const yn = /^\d+$/.test(y);
    if (xn && yn && Number(x) !== Number(y)) return sign(Number(x) - Number(y));
    if (xn !== yn) return xn ? -1 : 1;
    if (!xn && x !== y) return x < y ? -1 : 1;
  }
  return sign(as.length - bs.length);
}

/** The server's order of versions (releaseverify.CompareVersions): a prerelease ranks below
 * its release, and a `git describe` build after the tag it was built from (by its commits,
 * then dirty after clean). */
export function compareVersions(a: string, b: string): number {
  if (!VERSION_RE.test(a.trim()) || !VERSION_RE.test(b.trim())) return 0;
  const da = splitDescribe(a);
  const db = splitDescribe(b);
  const c = compareReleases(da.tag, db.tag);
  if (c !== 0) return c;
  if (da.commits !== db.commits) return sign(da.commits - db.commits);
  return da.dirty === db.dirty ? 0 : da.dirty ? 1 : -1;
}

/** Why a pin is refused ("" = fine): not a version, or below what is installed. */
export function pinProblem(pin: string, installed: string): "" | "bad_pin" | "pin_below_installed" {
  const p = pin.trim();
  if (p === "") return "";
  if (!validVersion(p)) return "bad_pin";
  if (installed && validVersion(installed) && compareVersions(p, installed) < 0) {
    return "pin_below_installed";
  }
  return "";
}
