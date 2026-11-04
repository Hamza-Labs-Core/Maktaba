// Epic 51 (story 51.10): typed calls for the components admin routes (story 51.08).
// Every route is admin-only on the server: no sign-in is 401, anyone else 403.
//
//   GET    /api/components              ListView (catalog order)
//   GET    /api/components/{id}         Detail: the view plus status parts and the last 20 changes
//   PUT    /api/components/{id}         {want, generation?, with_conflicts?} -> Detail
//   POST   /api/components/{id}/install want installed when absent; a wanted asset not on this
//                                        home (or whose install failed): asked again -> Detail
//   POST   /api/components/{id}/stop    no body: hold until start -> Detail
//   POST   /api/components/{id}/start   clears the hold -> Detail
//   POST   /api/components/{id}/restart no body: process and loop kinds -> Detail (or empty);
//                                        409 not_restartable (another kind), 409 not_running
//   POST   /api/components/{id}/settings/reset  its rules sections back to the defaults (51.13)
//   POST   /api/components/offline      {bundle?, components?, platform?} -> 202 {name, url} (51.16)
//   GET    /api/components/offline/{name}  202 {name, progress} while it builds, then the file
//   POST   /api/components/import       the offline file itself as the body -> {installed, changed}
//   POST   /api/components/preview      Request -> Preview (changes nothing)
//   POST   /api/components/apply        Request -> Result (all or nothing)
//
// Refusals are problem+json with a top-level `code` (stale_generation, component_required_by,
// component_conflicts, licence_missing, not_switchable, ...) and the extras named in 51.08.
import { ApiError, api, apiUrl, csrfHeaders, uploadWithProgress, type Problem } from "./api";

export type Want = "absent" | "installed" | "enabled";

export type Phase =
  | "not_installed"
  | "installing"
  | "removing"
  | "off"
  | "starting"
  | "running"
  | "degraded"
  | "draining"
  | "stopped"
  | "waiting"
  | "failed";

export interface Hold {
  by: string;
  at: string;
}

export interface Effect {
  id: string;
  from: string;
  to: string;
  reason?: string;
}

export interface MachinePhase {
  /** "home" or a cluster node id */
  machine: string;
  phase: string;
  reason?: string;
  observed_generation: number;
  health?: Health | null;
  /** the reason's params ({name}, {need}, ...), when the status carries them */
  reason_params?: Record<string, unknown> | null;
}

export interface Condition {
  type: string;
  status: "true" | "false" | "unknown" | string;
  reason?: string;
  params?: Record<string, unknown>;
  changed_at: string;
  generation: number;
}

/**
 * One component's health on one machine (story 51.14, api/internal/components/health.go).
 * `restarts_24h` is the supervisor's count over the last 24 hours; a server that sends only
 * `restarts` (since the supervisor started) is read through restartsOf.
 */
export interface Health {
  /** when it last started running (running or degraded only): its uptime */
  since?: string;
  restarts?: number;
  restarts_24h?: number;
  last_error_code?: string;
  last_error_at?: string;
  version?: string;
  rss_mb?: number;
  disk_mb?: number;
  /** the machine was not heard from for 90 s: the numbers may be old */
  stale: boolean;
  updated_at?: string;
}

/** The restarts of the last 24 hours, or the count since the supervisor started. */
export function restartsOf(h: Health): number {
  return h.restarts_24h ?? h.restarts ?? 0;
}

export interface StatusPart {
  machine: string;
  id: string;
  part: string;
  phase: string;
  conditions: Condition[];
  observed_generation: number;
  version?: string;
  detail?: Record<string, unknown>;
  since: string;
  updated_at: string;
}

export interface ComponentView {
  id: string;
  kind: string;
  group: string;
  title_key: string;
  desc_key: string;
  switchable: boolean;
  placement: string;
  licence?: string;
  licensed: boolean;
  /** each inner list is one requirement; more than one id means any one of them is enough */
  requires: string[][];
  wants: string[];
  conflicts: string[];
  dependents: string[];
  bundles: string[];
  features: string[];
  want: Want;
  hold: Hold | null;
  generation: number;
  has_row: boolean;
  phase: string;
  reason?: string;
  expected: Effect;
  machines: MachinePhase[];
  required_by: string[];
  install_size_mb: number;
  /** the catalog's list as it is: the rules.* sections and toml: keys it owns (story 51.13) */
  settings: string[];
  /** the home's health (story 51.14); absent until the home has reported it */
  health?: Health | null;
  /** the reason's params ({name}, {need}, ...), when the status carries them */
  reason_params?: Record<string, unknown> | null;
  /** this machine's last or running install or removal of an asset (story 51.16) */
  progress?: InstallProgress | null;
}

export interface AuditRow {
  id: number;
  at: string;
  action: string;
  actor_user_id?: string;
  actor_source?: string;
  payload: { before?: unknown; after?: unknown; by?: string; batch_id?: string } | null;
}

export interface ComponentDetail extends ComponentView {
  machines: (MachinePhase & { parts?: StatusPart[] })[];
  audit: AuditRow[];
}

export interface BundleLine {
  base: string;
  set: string;
  plus: string[];
  minus: string[];
}

export interface ListView {
  revision: number;
  bundle: BundleLine;
  components: ComponentView[];
}

export interface Change {
  id: string;
  want?: Want;
  /** true stops (holds) it, false starts it again */
  hold?: boolean;
  generation?: number;
}

export interface ChangeRequest {
  changes: Change[];
  with_conflicts?: boolean;
  generation?: number;
  bundle?: string;
}

export interface Refusal {
  id?: string;
  code: string;
  detail: string;
  required_by?: string[];
  conflicts?: string[];
  licence?: string;
  current?: { id: string; want: Want; hold?: Hold; generation: number };
  revision?: number;
}

export interface Preview {
  effects: Effect[];
  conflicts: string[];
  refused: Refusal[];
  queued_jobs_parked: number;
  drain_s: number;
}

export interface ApplyResult {
  batch_id?: string;
  revision: number;
  changed: { id: string; want: Want; hold?: Hold; generation: number }[];
  effect: Preview;
}

const path = (id: string) => `/api/components/${encodeURIComponent(id)}`;

export const componentsApi = {
  list: () => api.get<ListView>("/api/components"),
  show: (id: string) => api.get<ComponentDetail>(path(id)),
  setWant: (id: string, want: Want, generation: number, withConflicts = false) =>
    api.put<ComponentDetail>(path(id), {
      want,
      generation,
      ...(withConflicts ? { with_conflicts: true } : {}),
    }),
  install: (id: string) => api.post<ComponentDetail>(`${path(id)}/install`),
  /** a hold has no end time: the stop call sends no body at all */
  stop: (id: string) => api.post<ComponentDetail>(`${path(id)}/stop`),
  start: (id: string) => api.post<ComponentDetail>(`${path(id)}/start`),
  preview: (req: ChangeRequest) => api.post<Preview>("/api/components/preview", req),
  apply: (req: ChangeRequest) => api.post<ApplyResult>("/api/components/apply", req),
  /** process and loop kinds only; changes no desired state (so no generation and no preview) */
  restart: (id: string) => api.post<ComponentDetail | undefined>(`${path(id)}/restart`),
  /** story 51.13: deletes its rules sections; the page asks first */
  resetSettings: (id: string) => api.post<SettingsReset>(`${path(id)}/settings/reset`),
  /** story 51.16: builds an offline file on this machine; poll offlineState with the name */
  makeOffline: (req: OfflineRequest) =>
    api.post<{ name: string; url: string }>("/api/components/offline", req),
  offlineState,
  /** story 51.16: sends the offline file itself (application/x-tar), with the upload's progress */
  importFile: (file: Blob, name: string, onProgress?: (fraction: number) => void) =>
    uploadWithProgress<ImportResult>(
      `/api/components/import?name=${encodeURIComponent(name)}`,
      file,
      onProgress,
      "application/x-tar"
    ),
  /** story 51.17: the bundles and the one the home is based on */
  bundles: () => api.get<BundlesView>("/api/components/bundles"),
  /** story 51.17: the difference a bundle makes; without dry_run it is applied in one batch */
  bundle: (req: BundleRequest) => api.post<BundleDiff>("/api/components/bundle", req),
};

export type BundleId = "full" | "core" | "worker";

export interface SettingsReset {
  id: string;
  sections: { section: string; changed: number }[];
  changed: number;
}

/** The platforms an offline file can be made for (shared/components Platforms). */
export const OFFLINE_PLATFORMS = ["darwin-arm64", "windows-amd64", "linux-amd64", "linux-arm64"];

export interface OfflineRequest {
  bundle?: BundleId;
  components?: string[];
  /** empty: this machine's own */
  platform?: string;
}

/** The supervisor's progress of an install, a removal or an offline file (story 51.16). */
export interface InstallProgress {
  phase: string;
  percent: number;
  detail?: string;
  name?: string;
  done: number;
  total: number;
  reason?: string;
  params?: Record<string, unknown>;
  busy: boolean;
  /** whether the asset is on this machine now */
  installed?: boolean;
}

export interface ImportResult {
  installed: string[];
  changed: { id: string; want: Want; generation: number }[];
}

/** Where an offline file stands: still being built, or ready to save from `url`. */
export type OfflineState =
  | { ready: false; progress: InstallProgress | null }
  | { ready: true; url: string };

/**
 * GET /api/components/offline/{name}. While it builds the answer is 202 with its progress;
 * once built the same URL is the file, so the body is not read: the browser saves it from
 * the URL instead. A failed build or an unknown name is an ApiError with its code.
 */
async function offlineState(name: string): Promise<OfflineState> {
  const path = `/api/components/offline/${encodeURIComponent(name)}`;
  const ctl = new AbortController();
  const res = await fetch(apiUrl(path), {
    credentials: "include",
    signal: ctl.signal,
    headers: { Accept: "application/json", ...csrfHeaders() },
  });
  if (res.status === 202) {
    const body = (await res.json().catch(() => null)) as { progress?: InstallProgress } | null;
    return { ready: false, progress: body?.progress ?? null };
  }
  if (res.ok) {
    ctl.abort();
    return { ready: true, url: apiUrl(path) };
  }
  const problem = (await res.json().catch(() => null)) as Problem | null;
  throw new ApiError({
    type: "about:blank",
    title: res.statusText || "offline file",
    ...(problem ?? {}),
    status: res.status,
  });
}

export interface BundlesView {
  based_on: string;
  set: string;
  plus: string[];
  minus: string[];
  bundles: { id: BundleId; components: string[]; download_bytes: number }[];
}

export interface BundleRequest {
  bundle: BundleId;
  dry_run?: boolean;
  remove_unused?: boolean;
  generation?: number;
}

export interface BundleDiff {
  based_on: string;
  to: string;
  enable: string[];
  install: { id: string; bytes: number }[];
  disable: string[];
  remove: string[];
  download_bytes: number;
  revision: number;
  dry_run: boolean;
  effect: Preview;
  batch_id?: string;
}

/** The problem's `code` and extras of a components refusal, or null for any other error. */
export function refusalOf(e: unknown): Refusal | null {
  if (!(e instanceof ApiError)) return null;
  const p = e.problem as unknown as Partial<Refusal> & { code?: string };
  if (!p.code) return null;
  return { ...p, code: p.code, detail: p.detail ?? "" } as Refusal;
}
