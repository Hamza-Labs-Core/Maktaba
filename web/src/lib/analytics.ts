// Client contract for Epic 29 — Watch Analytics & Activity Tracking.
//
// Centralises the TypeScript shapes and the thin `analyticsApi` helpers
// every analytics surface reuses: the watch-session lifecycle the player
// drives (29.1), the per-user history (29.2) and activity/privacy (29.4),
// the admin dashboard (29.3), the per-video stats (29.5) and the export
// (29.6). One place to reconcile against the Go handlers.
import { api, request } from "./api";

// ─── Watch-session lifecycle (29.1) ────────────────────────────────────

export interface StartResponse {
  session_id?: string;
  tracking: boolean;
}
export interface SessionView {
  session_id: string;
  video_id: string;
  state: "active" | "completed" | "stopped" | "interrupted";
  duration_sec: number;
  percent_complete: number;
}

// ─── History (29.2) ────────────────────────────────────────────────────

export interface HistoryItem {
  video_id: string;
  title: string;
  duration_sec: number;
  times_watched: number;
  total_watch_sec: number;
  best_percent: number;
  last_watched_at: string;
  position_sec: number;
  completed: boolean;
}

// ─── Activity + privacy (29.4) ─────────────────────────────────────────

export interface ActivityItem {
  kind: "watched" | "searched" | "rated";
  at: string;
  meta: Record<string, unknown>;
}
export interface PrivacySettings {
  track_enabled: boolean;
}

// ─── Dashboard (29.3) ──────────────────────────────────────────────────

export type RangeKey = "today" | "7d" | "30d" | "90d" | "1y" | "all";
export type Bucket = "day" | "week" | "month";

export interface LiveSession {
  session_id: string;
  user_id: string;
  username: string;
  video_id: string;
  title: string;
  started_at: string;
  elapsed_sec: number;
  percent_complete: number;
  device_type?: string;
  platform?: string;
}
export interface CountStat {
  label: string;
  sessions: number;
  watch_sec: number;
}
export interface LabelStat {
  id: string;
  label: string;
  sessions: number;
  watch_sec: number;
}
export interface Summary {
  range: string;
  total_watch_sec: number;
  total_sessions: number;
  unique_viewers: number;
  completion_rate: number;
  devices: CountStat[];
  platforms: CountStat[];
  libraries: LabelStat[];
  genres: CountStat[];
}
export interface TopVideo {
  video_id: string;
  title: string;
  sessions: number;
  watch_sec: number;
  unique_viewers: number;
}
export interface TimePoint {
  bucket: string;
  watch_sec: number;
  sessions: number;
}
export interface ActivityResponse {
  bucket: Bucket;
  series: TimePoint[];
  heatmap: number[][]; // [7][24]
}
export interface ActiveUser {
  user_id: string;
  username: string;
  watch_sec: number;
  sessions: number;
  last_seen_at: string;
}

// ─── Per-video stats (29.5) ────────────────────────────────────────────

export interface Viewer {
  user_id: string;
  username: string;
  times_watched: number;
  total_watch_sec: number;
  best_percent: number;
  last_watched_at: string;
}
export interface VideoStats {
  total_views: number;
  unique_viewers: number;
  avg_completion: number;
  avg_watch_sec: number;
  completion_rate: number;
  last_watched_at?: string;
  viewers?: Viewer[];
}

// ─── helpers ───────────────────────────────────────────────────────────

const qs = (params: Record<string, string | number | undefined>): string => {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") u.set(k, String(v));
  }
  const s = u.toString();
  return s ? `?${s}` : "";
};

export const analyticsApi = {
  // lifecycle
  start: (body: { video_id: string; device_type?: string; platform?: string; quality?: string }) =>
    api.post<StartResponse>("/api/watch/start", body),
  heartbeat: (body: { session_id: string; position_sec: number }) =>
    api.post<SessionView>("/api/watch/heartbeat", body),
  stop: (body: { session_id: string; position_sec?: number }) =>
    api.post<SessionView>("/api/watch/stop", body),

  // history
  history: (params: { limit?: number; offset?: number; from?: string; to?: string } = {}) =>
    api.get<{ items: HistoryItem[]; limit: number; offset: number }>(
      `/api/me/history${qs(params)}`
    ),
  deleteHistory: (videoId: string) => api.delete<void>(`/api/me/history/${videoId}`),

  // activity + privacy
  activity: (params: { limit?: number; offset?: number; types?: string } = {}) =>
    api.get<{ items: ActivityItem[]; limit: number; offset: number }>(
      `/api/me/activity${qs(params)}`
    ),
  getPrivacy: () => api.get<PrivacySettings>("/api/me/activity/settings"),
  setPrivacy: (track_enabled: boolean) =>
    request<PrivacySettings>("/api/me/activity/settings", {
      method: "PUT",
      body: { track_enabled },
    }),

  // admin dashboard
  live: () => api.get<{ sessions: LiveSession[] }>("/api/admin/analytics/live"),
  summary: (range: RangeKey, refresh = false) =>
    api.get<Summary>(
      `/api/admin/analytics/summary${qs({ range, refresh: refresh ? "true" : undefined })}`
    ),
  topVideos: (range: RangeKey, limit = 10) =>
    api.get<{ videos: TopVideo[]; range: string }>(
      `/api/admin/analytics/top-videos${qs({ range, limit })}`
    ),
  activityStats: (range: RangeKey, bucket: Bucket = "day") =>
    api.get<ActivityResponse>(`/api/admin/analytics/activity${qs({ range, bucket })}`),
  users: (range: RangeKey, limit = 10) =>
    api.get<{ users: ActiveUser[]; range: string }>(
      `/api/admin/analytics/users${qs({ range, limit })}`
    ),
  exportUrl: (format: "csv" | "json", range: RangeKey) =>
    `/api/admin/analytics/export${qs({ format, range })}`,

  // per-video
  videoStats: (videoId: string) => api.get<VideoStats>(`/api/videos/${videoId}/stats`),
};

// formatWatchTime renders seconds as a compact human string (h/m).
export function formatWatchTime(sec: number): string {
  if (sec <= 0) return "0m";
  const h = Math.floor(sec / 3600);
  const m = Math.round((sec % 3600) / 60);
  if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`;
  return `${m}m`;
}

// formatPercent renders a 0..1 ratio or a 0..100 value as a percent.
export function formatPercentRatio(ratio: number): string {
  return `${Math.round(ratio * 100)}%`;
}
export function formatPercent(pct: number): string {
  return `${Math.round(pct)}%`;
}
