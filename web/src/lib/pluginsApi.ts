// Epic 54 (stories 54.07-54.13): typed calls for the admin plugin routes
// (api/internal/handlers/plugins/admin.go). Every route is admin-only on the server: no
// sign-in is 401, anyone else 403. Refusals are problem+json with a top-level `code`
// (plugin_required_capability, plugin_data_newer, reauth_required, settings_invalid, ...),
// whose words are plugins.error.<code>.
import { ApiError, api, uploadWithProgress } from "./api";

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
}

export interface PluginStage {
  name: string;
  queue: string;
  subject: string;
  version: number;
  pending: number;
  running: number;
  done_24h: number;
  failed_24h: number;
}

export interface PluginDetail extends Plugin {
  manifest: Record<string, unknown>;
  stages: PluginStage[];
  writes: number;
  data_rows: number;
  data_bytes: number;
  data_mb: number;
}

/** One of Maktaba's own built-in (first-party) plugins (Epic 54, story 54.22 §4). Its
 * component's switch on the Components page is its switch: there is no grants dialog. */
export interface BuiltInPlugin {
  id: string;
  name: string;
  name_ar: string;
  summary: string;
  summary_ar: string;
  component: string;
  stages: string[];
  /** fp.trusted.<capability> (and plugins.cap.<name>) words, each with its parameter */
  capabilities: { key: string; param: string }[];
}

export interface Policy {
  allow_unreviewed: boolean;
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

export const pluginsApi = {
  list: () =>
    api.get<{ plugins: Plugin[]; policy: Policy; built_in?: BuiltInPlugin[] }>("/api/plugins"),
  show: (id: string) => api.get<PluginDetail>(`/api/plugins/${enc(id)}`),
  install: (f: InstallForm, onProgress?: (fraction: number) => void) =>
    uploadWithProgress<InstallResult>("/api/plugins/install", installBody(f), onProgress),
  update: (id: string, f: InstallForm, onProgress?: (fraction: number) => void) =>
    uploadWithProgress<InstallResult>(`/api/plugins/${enc(id)}/update`, installBody(f), onProgress),
  grants: (id: string, g: Grants) => api.put<Grants>(`/api/plugins/${enc(id)}/grants`, g),
  enable: (id: string) => api.post<PluginDetail>(`/api/plugins/${enc(id)}/enable`),
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
