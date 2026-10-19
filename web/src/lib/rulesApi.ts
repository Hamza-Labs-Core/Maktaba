// Client for the rules API (detection, grouping and channel planning):
//   GET   /api/rules            → { sections: RuleSection[] }
//   PATCH /api/rules/{section}  { set: {name: value}, reset: [names] }
//         → 200 { section, changed } | 400 { error: "<field>: <message>", field }
// PATCH is admin only (403 otherwise).
import { api, ApiError } from "./api";

export interface RuleField {
  name: string;
  kind: string;
  group: string;
  label: string;
  help: string;
  default: unknown;
  value: unknown;
  changed: boolean;
  invalid?: string;
  minimum?: number;
  /** the largest value a number may take (with a minimum: shown as a slider) */
  maximum?: number;
  choices?: string[];
  columns?: string[];
  column_kinds?: string[];
  time_columns?: number[];
  /** per column: a row may leave it out (its default applies) */
  column_optional?: boolean[];
}

export interface RuleSection {
  key: string;
  title: string;
  what: string;
  unknown?: string[];
  fields: RuleField[];
}

export interface RulesResponse {
  sections: RuleSection[];
}

export interface RulesPatchResult {
  section: string;
  changed: string[];
}

export const rulesApi = {
  list: () => api.get<RulesResponse>("/api/rules"),
  patch: (section: string, set: Record<string, unknown>, reset: string[]) =>
    api.patch<RulesPatchResult>(`/api/rules/${encodeURIComponent(section)}`, { set, reset }),
};

/** The message and the setting a failed PATCH names (400 {error, field}). */
export function rulesError(e: unknown): { message: string; field: string | null } {
  if (e instanceof ApiError) {
    const body = e.problem as unknown as { error?: string; field?: string; detail?: string };
    const message = body.error ?? body.detail ?? e.message;
    let field = body.field ?? null;
    if (!field && (e.status === 400 || e.status === 422)) {
      const m = /^([\w.[\]]+): /.exec(message);
      if (m) field = m[1];
    }
    // "themes[0].number" → the setting is "themes"
    return { message, field: field ? field.split(/[.[]/)[0] : null };
  }
  return { message: e instanceof Error ? e.message : String(e), field: null };
}
