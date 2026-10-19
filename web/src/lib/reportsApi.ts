// Typed client for the reports the pipeline writes (review pages, channel plan,
// its preview, channel audit) and the plan preview. The pages themselves are
// loaded by an iframe from `reportsApi.pageUrl(name)`.
import { api, apiUrl } from "./api";

export type ReportKind = "review" | "plan" | "preview" | "audit" | "other";

export interface Report {
  name: string;
  kind: ReportKind;
  title: string;
  library_id: string | null;
  size: number;
  updated_at: string;
}

export type PreviewKind = "preview" | "stored";

export interface PlanPreviewSummary {
  channels?: number;
  lanes?: number;
  changes?: { added: number; removed: number; changed: number; reordered: number };
  added?: string[];
  removed?: string[];
  changed?: string[];
}

export interface PlanPreviewStatus {
  running: boolean;
  kind: PreviewKind | null;
  phase: string;
  error?: string;
  started_at: number | null;
  finished_at: number | null;
  summary: PlanPreviewSummary;
  log: string[];
  ready: { preview: boolean; plan: boolean };
}

/** The file names the pipeline gives its well-known pages. */
export const PLAN_PAGE = "channel-plan.html";
export const PREVIEW_PAGE = "channel-plan-preview.html";

export const reportsApi = {
  list: () => api.get<{ reports: Report[] }>("/api/reports"),
  pageUrl: (name: string, version?: number) =>
    apiUrl(`/api/reports/${encodeURIComponent(name)}${version ? `?v=${version}` : ""}`),
  previewStatus: () => api.get<PlanPreviewStatus>("/api/reports/channel-plan/preview"),
  startPreview: (kind: PreviewKind) =>
    api.post<{ started: boolean; kind: PreviewKind }>("/api/reports/channel-plan/preview", {
      kind,
    }),
};
