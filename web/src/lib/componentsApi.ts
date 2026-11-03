// Epic 51 (story 51.10): typed calls for the components admin routes (story 51.08).
// Every route is admin-only on the server: no sign-in is 401, anyone else 403.
//
//   GET    /api/components              ListView (catalog order)
//   GET    /api/components/{id}         Detail: the view plus status parts and the last 20 changes
//   PUT    /api/components/{id}         {want, generation?, with_conflicts?} -> Detail
//   POST   /api/components/{id}/install want installed when absent -> Detail
//   POST   /api/components/{id}/stop    no body: hold until start -> Detail
//   POST   /api/components/{id}/start   clears the hold -> Detail
//   POST   /api/components/preview      Request -> Preview (changes nothing)
//   POST   /api/components/apply        Request -> Result (all or nothing)
//
// Refusals are problem+json with a top-level `code` (stale_generation, component_required_by,
// component_conflicts, licence_missing, not_switchable, ...) and the extras named in 51.08.
import { ApiError, api } from "./api";

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
}

export interface Condition {
  type: string;
  status: "true" | "false" | "unknown" | string;
  reason?: string;
  params?: Record<string, unknown>;
  changed_at: string;
  generation: number;
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
  /** the rules.* sections and toml: keys it owns, when the API sends them (story 51.13) */
  settings?: string[];
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
  /** story 51.17: the bundles and the one the home is based on */
  bundles: () => api.get<BundlesView>("/api/components/bundles"),
  /** story 51.17: the difference a bundle makes; without dry_run it is applied in one batch */
  bundle: (req: BundleRequest) => api.post<BundleDiff>("/api/components/bundle", req),
};

export type BundleId = "full" | "core" | "worker";

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
