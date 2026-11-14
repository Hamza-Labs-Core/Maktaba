// Epic 54 (stories 54.07-54.13): typed calls for the admin plugin routes
// (api/internal/handlers/plugins/admin.go). Every route is admin-only on the server: no
// sign-in is 401, anyone else 403. Refusals are problem+json with a top-level `code`
// (plugin_required_capability, plugin_data_newer, reauth_required, settings_invalid, ...),
// whose words are plugins.error.<code>.
import { ApiError, api, uploadWithProgress } from "./api";
import type { PluginIsolationLevel } from "./serverSettingsApi";

export type ReviewLevel = "none" | "developer" | "automated" | "reviewed" | "store" | "development";

export interface PluginNames {
  name: Record<string, string>;
  summary: Record<string, string>;
}

export interface Grants {
  capabilities: string[];
  libraries: string[];
  all_libraries: boolean;
  generation?: number;
  granted_by?: string;
  granted_at?: string;
}

export interface PluginHealth {
  tripped: boolean;
  tripped_at?: string;
  trip_reason?: string;
  jobs_done: number;
  jobs_failed: number;
  last_error_code?: string;
  last_error_at?: string;
  recent: { at: number; cause: string; ms: number; memory: number }[] | null;
}

export interface Plugin {
  id: string;
  component: string;
  version: string;
  digest: string;
  names: PluginNames;
  review_level: ReviewLevel;
  rating: number;
  installed_at: string;
  want: string;
  held_by?: string;
  generation: number;
  phase: string;
  reason?: string;
  reason_params?: Record<string, string> | null;
  kept_off?: string | null;
  grants: Grants;
  required: string[];
  optional: string[];
  previous_version?: string;
  health: PluginHealth;
  /** A store `disable` covers it (story 54.17 §3); revocation is the entry that applies. */
  revoked?: boolean;
  revocation?: {
    action: "disable" | "warn" | "delist";
    reason_code: string;
    versions: string;
    issued: string;
  } | null;
  auto_update?: boolean;
  /** Story 54.44, the same for every plugin: who publishes it ("maktaba", verified, or ""). */
  publisher?: { id: string; verified: boolean };
  /** where this home got it: with the Maktaba release, from the store, or from a file */
  source?: PluginSource;
  stage_names?: string[];
  /** its stages as the queue names them (the failures view's stage filter) */
  queues?: string[];
  /** why it cannot be turned on from its switch (a components.reason.<code>, or "held");
   * absent when it can. Turning it off is never locked. */
  switch_locked?: string;
  activity?: PluginActivity;
  resources?: PluginResources;
  /** the settings its component owns (rules.<section>, toml:<key>): Maktaba's own pages */
  settings?: string[];
}

export type PluginSource = "release" | "store" | "file";

/** A plugin's jobs over its stages (story 54.44). */
export interface PluginActivity {
  pending: number;
  running: number;
  done_24h: number;
  failed_24h: number;
  /** failed jobs open now: what the failures view lists */
  problems: number;
  avg_ms?: number;
  p95_ms?: number;
}

/** One process that runs a plugin's stages on one machine, and its memory. A runner group,
 * the pipeline worker and the plugin host are shared: the number is the whole process's. */
export interface ProcessUse {
  process: string;
  /** light, files, net, service, ml, index; "worker" (the pipeline worker); "host" */
  group: string;
  component: string;
  machine: string;
  phase?: string;
  rss_mb?: number;
  /** what rss_mb measures: this process alone, or every process of its component */
  scope?: "process" | "component";
  stages: string[] | null;
  shared_with: string[] | null;
  shared_core: boolean;
}

export interface PluginResources {
  processes: ProcessUse[] | null;
  shared: boolean;
  data_rows: number;
  data_bytes: number;
  data_mb: number;
  memory_mb: number;
  peak_memory_mb?: number;
}

/** A plugin's status on one machine. */
export interface PluginMachine {
  machine: string;
  phase: string;
  reason?: string;
  reason_params?: Record<string, string> | null;
  restarts_24h: number;
  last_error_code?: string;
  stale: boolean;
}

/** What turning a plugin off does beside stopping it (GET /api/plugins/{id}/downstream). */
export interface SwitchEffect {
  /** other plugins' stages that go idle (54.39 §6) */
  stages: string[] | null;
  /** components the switch moves (waiting: needs this one; degraded: runs with less) */
  also: { component: string; title_key: string; plugin?: string; to?: string }[] | null;
  processes: string[] | null;
  loops: string[] | null;
  features: string[] | null;
  /** stages of other plugins (or of Maktaba itself) its stages hand work to */
  fed?: { stage: string; label?: string; plugin?: string }[] | null;
  queued_jobs_parked: number;
  drain_s: number;
}

/** One version in the store's catalog. */
export interface CatalogVersion {
  version: string;
  sha256: string;
  size: number;
  maktaba: string;
  plugin_api: string;
  platforms: string[];
  capabilities: string[];
  review_level: "automated" | "reviewed";
  released: string;
}

/** A plugin as Browse shows it (GET /api/plugins/catalog, story 54.18 §1). */
export interface CatalogEntry {
  id: string;
  developer: string;
  names: Record<string, string>;
  summaries: Record<string, string>;
  category: string;
  tags: string[];
  rating_avg_x100: number;
  rating_count: number;
  installs: number;
  machine_translated: string[];
  /** "free", or the product the store sells ("one_time" or "yearly", story 54.19 §4) */
  price?: string;
  versions: CatalogVersion[];
  /** the listing's other words; a paid plugin's prices in minor units by currency (prices) */
  extra?: Record<string, unknown>;
  newest: CatalogVersion | null;
  installed?: string;
  update?: string;
  auto_update: boolean;
  withdrawn?: { action: string; reason_code: string } | null;
}

export interface CatalogView {
  plugins: CatalogEntry[];
  trust: TrustStatus;
  opening: boolean;
}

/** The store's signed lists on this home (GET /api/plugins/trust). */
export interface TrustStatus {
  revoked_version: number;
  revoked_issued?: string;
  revoked_expired: boolean;
  /** MAKTABA_OFFLINE: the lists are not fetched (they age; plugins keep running) */
  offline?: boolean;
  catalog_version: number;
  catalog_issued?: string;
  catalog_expired: boolean;
  store_keys: boolean;
  store_installs_ok: boolean;
  last_error?: string;
}

export interface PluginStage {
  name: string;
  /** the stage's name for people, from its manifest */
  label?: string;
  queue: string;
  subject: string;
  version: number;
  pending: number;
  running: number;
  done_24h: number;
  failed_24h: number;
  problems?: number;
  avg_ms?: number;
  p95_ms?: number;
  /** a runner group, "worker" or "host" */
  runs_in?: string;
}

export interface PluginDetail extends Plugin {
  manifest: Record<string, unknown>;
  stages: PluginStage[];
  writes: number;
  data_rows: number;
  data_bytes: number;
  data_mb: number;
  /** an admin ticked "Include its logs in problem reports" (54.09 §3) */
  report_logs?: boolean;
  /** what a Maktaba-published plugin can do: fp.trusted.<capability> words with a parameter */
  capabilities?: { key: string; param: string }[] | null;
  machines?: PluginMachine[] | null;
}

export interface Policy {
  allow_unreviewed: boolean;
  /** Epic 59 story 59.07: set through setIsolation, never through setPolicy */
  allow_container_isolation?: boolean;
}

/** GET /api/plugins/isolation (Epic 59 story 59.07). */
export interface PluginIsolation {
  /** the home's: container when any machine is ("" before any server wrote its level) */
  level: "full" | "container" | "";
  machines: ({ machine: string } & PluginIsolationLevel)[];
  allow_container_isolation: boolean;
  /** third-party plugins wait for the admin's yes now */
  held: boolean;
}

export interface PluginWrite {
  id: number;
  version: string;
  job_id?: number;
  target: string;
  subject_id: string;
  field: string;
  before: unknown;
  after: unknown;
  at: string;
  reverted_at?: string;
}

export interface SettingProperty {
  name: string;
  type: "string" | "integer" | "number" | "boolean" | "array";
  default: unknown;
  enum?: string[];
  items_enum?: string[];
  minimum?: number;
  maximum?: number;
  max_length?: number;
  pattern?: string;
  title_key: string;
  help_key?: string;
  widget?: string;
}

export interface PluginSettings {
  schema: { properties: SettingProperty[] } | null;
  values: Record<string, unknown>;
  strings: Record<string, Record<string, string>>;
  reset?: string[];
}

export interface InstallForm {
  file: Blob;
  fileName: string;
  grants: string[];
  libraries: string[];
  allLibraries: boolean;
  enable: boolean;
  confirmName: string;
}

export interface InstallResult {
  result: {
    plugin_id: string;
    version: string;
    already_installed: boolean;
    updated: boolean;
    kept_off?: string;
    enabled: boolean;
    enable_error?: string;
  };
  plugin: PluginDetail;
}

const enc = encodeURIComponent;

/** The form fields go first, the file last: the server streams the file to the supervisor. */
function installBody(f: InstallForm): FormData {
  const form = new FormData();
  form.append("grants", JSON.stringify(f.grants));
  form.append("libraries", JSON.stringify(f.libraries));
  form.append("all_libraries", String(f.allLibraries));
  form.append("enable", String(f.enable));
  form.append("confirm_name", f.confirmName);
  form.append("file", f.file, f.fileName);
  return form;
}

/** A flow check's finding, with its words (54.39). */
export interface FlowFinding {
  code: string;
  severity: "refuse" | "warn";
  plugin?: string;
  detail?: string;
  path?: string[];
  message: string;
  message_ar: string;
}

/** The normalised graph a chart draws (54.39 §3). */
export interface FlowGraph {
  nodes: {
    id: string;
    kind: string;
    name: string;
    plugin: string;
    state: string;
    subject: string;
  }[];
  edges: { from: string; kind: string; to: string }[];
}

/** GET /api/plugins/{id}/flow: the plugin's flow as this home has it (54.41). */
export interface PluginFlow {
  id: string;
  version: string;
  name: string;
  graph: FlowGraph;
  missing: { id: string; version: string }[];
  findings: FlowFinding[] | null;
}

/** The link of this home to a plugin store account (story 54.15 §5). */
export interface StoreLinkStatus {
  linked: boolean;
  home_id?: string;
  linked_at?: string;
  store: string;
  pending?: { user_code: string; verification_uri: string; expires_at: string; interval: number };
}

/** A paid plugin's entitlement this home holds (story 54.19 §6), checked offline. */
export interface HeldEntitlement {
  plugin_id: string;
  product: "one_time" | "yearly";
  purchase_id: string;
  valid_until: string;
  refreshed_at: string;
}

/** A plugin that declares panels, in the order pages show them (story 54.20 §1). */
export interface PanelPlugin {
  id: string;
  names: PluginNames;
  on: string[];
}

export const pluginsApi = {
  panelOrder: () =>
    api.get<{ plugins: PanelPlugin[]; order: string[] }>("/api/plugins/panel-order"),
  setPanelOrder: (order: string[]) =>
    api.put<{ order: string[] }>("/api/plugins/panel-order", { order }),
  trust: () => api.get<TrustStatus>("/api/plugins/trust"),
  catalog: (all = false) => api.get<CatalogView>(`/api/plugins/catalog${all ? "?all=true" : ""}`),
  installFromStore: (id: string, version: string) =>
    api.post<InstallResult>("/api/plugins/install", { store: { id, version } }),
  rate: (id: string, stars: number, text: string) =>
    api.post(`/api/plugins/${enc(id)}/rate`, { stars, text }),
  /** attachLogs: the plugin's own recent lines, scrubbed, go with the report (#758) */
  report: (id: string, reason: string, details: string, attachLogs = false) =>
    api.post(`/api/plugins/${enc(id)}/report`, { reason, details, attach_logs: attachLogs }),
  setAutoUpdate: (id: string, on: boolean) =>
    api.put<PluginDetail>(`/api/plugins/${enc(id)}/auto-update`, { on }),
  storeStatus: () => api.get<StoreLinkStatus>("/api/plugins/store-account"),
  /** A one-time purchase intent for the store's buy page (story 54.19 §5). */
  buy: (pluginId: string, lang: "en" | "ar") =>
    api.post<{ buy_url: string; intent: string; product: string; expires_at: string }>(
      "/api/plugins/store-account/buy",
      { plugin_id: pluginId, lang }
    ),
  storeLink: () =>
    api.post<NonNullable<StoreLinkStatus["pending"]>>("/api/plugins/store-account/link"),
  storePoll: () => api.post<StoreLinkStatus>("/api/plugins/store-account/poll"),
  storeUnlink: () => api.delete<{ linked: boolean }>("/api/plugins/store-account"),
  entitlements: () =>
    api.get<{ entitlements: HeldEntitlement[] }>("/api/plugins/store-account/entitlements"),
  refreshEntitlements: (afterBuy = false) =>
    api.post<{ linked: boolean; valid: number }>(
      "/api/plugins/store-account/entitlements/refresh",
      {
        after_buy: afterBuy,
      }
    ),
  /** release: also the plugins Maktaba publishes that came with this release (the Plugins
   * page asks for them; without it the list is the installed plugins, as it always was) */
  list: (release = false) =>
    api.get<{
      plugins: Plugin[];
      policy: Policy;
      /** parts of Maktaba itself: always on, not plugins */
      core?: { component: string; title_key: string }[];
    }>(release ? "/api/plugins?release=true" : "/api/plugins"),
  graph: () => api.get<{ graph: FlowGraph; findings: FlowFinding[] | null }>("/api/plugins/graph"),
  flow: (id: string) => api.get<PluginFlow>(`/api/plugins/${enc(id)}/flow`),
  downstream: (id: string) => api.get<SwitchEffect>(`/api/plugins/${enc(id)}/downstream`),
  show: (id: string) => api.get<PluginDetail>(`/api/plugins/${enc(id)}`),
  setReportLogs: (id: string, include: boolean) =>
    api.put<{ plugin_id: string; report_logs: boolean }>(`/api/plugins/${enc(id)}/report-logs`, {
      include,
    }),
  install: (f: InstallForm, onProgress?: (fraction: number) => void) =>
    uploadWithProgress<InstallResult>("/api/plugins/install", installBody(f), onProgress),
  update: (id: string, f: InstallForm, onProgress?: (fraction: number) => void) =>
    uploadWithProgress<InstallResult>(`/api/plugins/${enc(id)}/update`, installBody(f), onProgress),
  grants: (id: string, g: Grants) => api.put<Grants>(`/api/plugins/${enc(id)}/grants`, g),
  /** withConflicts: also turn off what it cannot run with (the answer to component_conflicts) */
  enable: (id: string, withConflicts = false) =>
    withConflicts
      ? api.post<PluginDetail>(`/api/plugins/${enc(id)}/enable`, { with_conflicts: true })
      : api.post<PluginDetail>(`/api/plugins/${enc(id)}/enable`),
  disable: (id: string) => api.post<PluginDetail>(`/api/plugins/${enc(id)}/disable`),
  start: (id: string) => api.post<PluginDetail>(`/api/plugins/${enc(id)}/start`),
  stop: (id: string) => api.post<PluginDetail>(`/api/plugins/${enc(id)}/stop`),
  rollback: (id: string, clearData: boolean) =>
    api.post<PluginDetail>(
      `/api/plugins/${enc(id)}/rollback${clearData ? "?clear_data=true" : ""}`
    ),
  remove: (id: string, purge: boolean) =>
    api.delete<{ id: string; queued_jobs_dropped: number; writes_reverted?: number }>(
      `/api/plugins/${enc(id)}${purge ? "?purge=true" : ""}`
    ),
  writes: (id: string, before?: number) =>
    api.get<{ writes: PluginWrite[] }>(
      `/api/plugins/${enc(id)}/writes${before ? `?before=${before}` : ""}`
    ),
  revert: (id: string, ids: number[]) =>
    api.post<{ reverted: number }>(`/api/plugins/${enc(id)}/writes/revert`, { ids }),
  settings: (id: string) => api.get<PluginSettings>(`/api/plugins/${enc(id)}/settings`),
  saveSettings: (id: string, values: Record<string, unknown>) =>
    api.put<PluginSettings>(`/api/plugins/${enc(id)}/settings`, values),
  policy: () => api.get<Policy>("/api/plugins/policy"),
  setPolicy: (p: Policy, password?: string) =>
    api.put<Policy>("/api/plugins/policy", { ...p, ...(password ? { password } : {}) }),
  isolation: () => api.get<PluginIsolation>("/api/plugins/isolation"),
  /** Allow plugins with container-only isolation: turning it on asks for the password. */
  setIsolation: (allow: boolean, password?: string) =>
    api.put<PluginIsolation>("/api/plugins/isolation", {
      allow,
      ...(password ? { password } : {}),
    }),
};

/** The refusal's code of an error from these routes ("" when it is not a refusal). */
export function pluginErrorCode(e: unknown): string {
  if (!(e instanceof ApiError)) return "";
  const p = e.problem as unknown as { code?: string } | undefined;
  return p?.code ?? "";
}

/** A plugin's own word in the admin's language, English when its language has none. */
export function pluginWord(words: Record<string, string> | undefined, locale: string): string {
  if (!words) return "";
  return words[locale] || words.en || "";
}
