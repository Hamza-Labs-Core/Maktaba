// Client contract for Epic 29 — Watch Analytics & Activity Tracking.
//
// Centralises the TypeScript shapes and the thin `analyticsApi` helpers
// every analytics surface reuses: the watch-session lifecycle the player
// drives (29.1), the per-user history (29.2) and activity/privacy (29.4),
// the admin analytics page (29.3, rebuilt by Epic 44: plays, Live TV
// viewings and the apps' use), the per-video stats (29.5) and the export
// (29.6). One place to reconcile against the Go handlers.
import { api, request, postBeacon } from "./api";
import { isDesktop } from "./desktop";
import { isNativeRuntime } from "./native";

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

// ─── Admin analytics page (29.3, rebuilt by Epic 44 story 44.7) ───────

// The ranges the page offers; the server also takes 1y and all.
export type RangeKey = "today" | "7d" | "30d" | "90d";
export type Bucket = "hour" | "day" | "week" | "month";
// The ways of watching: a play of a video in an app, a viewing of a channel
// in an app, through Plex's Live TV (the tuner) or on the remote TV.
export type Surface = "video" | "app" | "tuner" | "remote";
export const SURFACES: Surface[] = ["video", "app", "tuner", "remote"];

// AnalyticsQuery is the range (a preset, or from/to dates) and the filters
// every endpoint takes.
export interface AnalyticsQuery {
  range?: RangeKey;
  from?: string; // YYYY-MM-DD
  to?: string; // YYYY-MM-DD, inclusive
  library?: string;
  user?: string;
  client_app?: string;
  surface?: Surface | "";
}

export interface CountStat {
  label: string;
  sessions: number;
  watch_sec: number;
  people: number;
}
export interface LabelStat extends CountStat {
  id: string;
}
export interface Summary {
  range: string;
  from?: string;
  to?: string;
  watch_sec: number;
  video_sec: number;
  tv_sec: number;
  live_tv_hours: number;
  plays: number;
  viewings: number;
  people: number;
  completion_rate: number;
  surfaces: CountStat[];
  apps: CountStat[];
  platforms: CountStat[];
  devices: CountStat[];
}
export interface NowPlay {
  session_id: string;
  user_id: string;
  username: string;
  video_id: string;
  title: string;
  started_at: string;
  elapsed_sec: number;
  percent_complete: number;
  client_app: string;
  client_version: string;
  platform: string;
  device_type: string;
}
export interface NowViewing {
  id: string;
  surface: Surface;
  channel_id: string;
  channel_name: string;
  channel_number: number;
  user_id: string;
  username: string;
  started_at: string;
  elapsed_sec: number;
  client_app: string;
  client_version: string;
  platform: string;
  device_type: string;
}
export interface NowResponse {
  plays: NowPlay[];
  viewings: NowViewing[];
  counts: Partial<Record<Surface, number>>;
  at: string;
}
export interface TopVideo {
  video_id: string;
  title: string;
  sessions: number;
  watch_sec: number;
  unique_viewers: number;
  completion_rate: number;
}
export interface TopChannel {
  channel_id: string;
  name: string;
  number: number;
  viewings: number;
  watch_sec: number;
  people: number;
  peak_viewers: number;
  surfaces: CountStat[];
}
export interface TopChannelsResponse {
  channels: TopChannel[];
  surfaces: CountStat[];
  peak_viewers: number;
  peak_at?: string;
}
export interface TimePoint {
  bucket: string;
  watch_sec: number;
  video_sec: number;
  tv_sec: number;
  plays: number;
  viewings: number;
}
export interface ActivityResponse {
  bucket: Bucket;
  series: TimePoint[];
  heatmap: number[][]; // [7][24]
}
export interface Person {
  user_id: string;
  username: string;
  watch_sec: number;
  video_sec: number;
  tv_sec: number;
  plays: number;
  viewings: number;
  channels: number;
  apps: string[];
  last_watched_at?: string;
  last_active_day?: string;
}
export interface PlatformUsage {
  platform: string;
  requests: number;
  errors: number;
  error_rate: number;
  slow: number;
  avg_ms: number;
}
export interface AppUsage extends Omit<PlatformUsage, "platform"> {
  client_app: string;
  people: number;
  versions: { version: string; people: number }[];
  platforms: PlatformUsage[];
}
export interface UsagePoint {
  day: string;
  requests: number;
  errors: number;
  people: number;
}
export interface AppsResponse {
  apps: AppUsage[];
  trend: UsagePoint[];
  platforms: CountStat[];
  devices: CountStat[];
}
export interface RemoteDay {
  day: string;
  minutes_delivered: number;
  simulcast_minutes: number;
  cost_usd: number;
}
export interface RemoteTVResponse {
  minutes_delivered: number;
  simulcast_minutes: number;
  cost_usd: number;
  viewings: number;
  watch_sec: number;
  people: number;
  days: RemoteDay[];
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
  // stopBeacon is the page-unload variant of stop(): a keepalive POST
  // that still rides the session cookie + CSRF header, so the final
  // position lands even as the tab is closing (see api.postBeacon).
  stopBeacon: (body: { session_id: string; position_sec?: number }) =>
    postBeacon("/api/watch/stop", body),

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

  // admin analytics page: every call takes the range and the filters
  summary: (q: AnalyticsQuery, refresh = false) =>
    api.get<Summary>(
      `/api/admin/analytics/summary${qs({ ...q, refresh: refresh ? "true" : undefined })}`
    ),
  now: (q: AnalyticsQuery) => api.get<NowResponse>(`/api/admin/analytics/now${qs({ ...q })}`),
  activityStats: (q: AnalyticsQuery, bucket?: Bucket) =>
    api.get<ActivityResponse>(`/api/admin/analytics/activity${qs({ ...q, bucket })}`),
  topVideos: (q: AnalyticsQuery, limit = 10) =>
    api.get<{ videos: TopVideo[] }>(`/api/admin/analytics/top-videos${qs({ ...q, limit })}`),
  topChannels: (q: AnalyticsQuery, limit = 10) =>
    api.get<TopChannelsResponse>(`/api/admin/analytics/top-channels${qs({ ...q, limit })}`),
  libraries: (q: AnalyticsQuery) =>
    api.get<{ libraries: LabelStat[] }>(`/api/admin/analytics/libraries${qs({ ...q })}`),
  genres: (q: AnalyticsQuery) =>
    api.get<{ genres: CountStat[] }>(`/api/admin/analytics/genres${qs({ ...q })}`),
  people: (q: AnalyticsQuery, limit = 100) =>
    api.get<{ people: Person[] }>(`/api/admin/analytics/people${qs({ ...q, limit })}`),
  apps: (q: AnalyticsQuery) => api.get<AppsResponse>(`/api/admin/analytics/apps${qs({ ...q })}`),
  remoteTV: (q: AnalyticsQuery) =>
    api.get<RemoteTVResponse>(`/api/admin/analytics/remote-tv${qs({ ...q })}`),
  exportUrl: (format: "csv" | "json", q: AnalyticsQuery) =>
    `/api/admin/analytics/export${qs({ format, ...q })}`,

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

// ─── client fingerprint for watch sessions (29.1) ──────────────────────

export interface ClientInfo {
  device_type: string; // "desktop" | "mobile" | "web"
  platform: string; // "macos" | "windows" | "linux" | "ios" | "android" | "browser"
}

// platformFromUA derives a coarse OS label from a user-agent string.
// Exported (and pure) so it is unit-testable without a real navigator.
export function platformFromUA(ua: string): string {
  const s = ua.toLowerCase();
  if (/iphone|ipad|ipod/.test(s)) return "ios";
  if (/android/.test(s)) return "android";
  if (/mac os|macintosh/.test(s)) return "macos";
  if (/windows/.test(s)) return "windows";
  if (/linux/.test(s)) return "linux";
  return "browser";
}

// detectClient classifies the runtime so the dashboard can split watch
// time by shell. The same web bundle runs in three shells:
//   - Tauri desktop  → device_type "desktop"  (isDesktop)
//   - Capacitor mobile → device_type "mobile" (isNativeRuntime)
//   - plain browser  → device_type "web"
// The OS label comes from the UA in every case. Values are kept short:
// the server truncates device_type/platform to 32 chars.
export function detectClient(): ClientInfo {
  const ua = typeof navigator !== "undefined" ? navigator.userAgent : "";
  const platform = platformFromUA(ua);
  let device_type = "web";
  if (isDesktop()) device_type = "desktop";
  else if (isNativeRuntime()) device_type = "mobile";
  return { device_type, platform };
}
