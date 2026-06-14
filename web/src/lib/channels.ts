// Shared client contract for Epic 27 — Live Channels & TV programming.
//
// The channel engine itself (CRUD + scheduler + live stream + guide
// exports) lands on the server in Epic 27 batch 1 (api/streaming/pipeline,
// slots 0081–0084). This module is the *client* side: the TypeScript
// shapes and the thin `api` helpers every receiver surface reuses — the
// EPG grid (27.6), the live player (27.7), the admin builder (27.8), the
// "What's On Now" home rail (27.9) and the filler/bumper admin (27.10).
//
// Centralising the contract here means there is exactly one place to
// reconcile against the Go handlers once the backend merges — no surface
// hard-codes a path or a field name of its own.
import { api } from "./api";

// ─── Channel ──────────────────────────────────────────────────────────
// Mirrors the `channels` row (slot 0081). `mode` selects which programming
// rule the scheduler applies; `mode_config`/`source_filter` carry the
// per-mode parameters opaquely so the client never has to know the
// scheduler's internals.
export type ChannelMode = "shuffle" | "marathon" | "schedule" | "smart_mix";
export type ChannelTransition = "cut" | "crossfade";

export interface Channel {
  id: string;
  number: number;
  name: string;
  slug?: string;
  logo_path?: string | null;
  category?: string | null;
  mode: ChannelMode;
  mode_config?: Record<string, unknown>;
  source_filter?: Record<string, unknown>;
  enabled: boolean;
  sort_order: number;
  transition?: ChannelTransition;
  library_id?: string;
  updated_at?: string;
}

// ─── Program block ────────────────────────────────────────────────────
// One `channel_programs` block. `kind` distinguishes a real program from
// the filler/bumper the scheduler injects to keep the wall-clock timeline
// contiguous; `start_at`/`end_at` are absolute (the wall-clock anchoring
// invariant) so every guide surface derives "is airing"/progress purely
// from `now` vs these timestamps.
export type ProgramKind = "program" | "filler" | "bumper" | "station_id" | "up_next";

export interface Program {
  channel_id: string;
  seq: number;
  kind: ProgramKind;
  video_id?: string | null;
  title: string;
  start_at: string;
  end_at: string;
  poster_url?: string | null;
  description?: string | null;
  series?: string | null;
  episode?: string | null;
}

export interface GuideChannel {
  channel: Channel;
  programs: Program[];
}

// The guide/now payloads always carry `server_time` so the client can
// reconcile a wrong local clock against the server (27.6 EC4 / 27.9 EC2).
export interface GuideResponse {
  server_time: string;
  channels: GuideChannel[];
}

export interface NowEntry {
  channel: Channel;
  current?: Program | null;
  next?: Program | null;
}

export interface NowResponse {
  server_time: string;
  items: NowEntry[];
}

export interface TuneResponse {
  session_id: string;
  manifest_url?: string;
  direct_url?: string;
  channel_id: string;
}

// ─── Filler & bumpers (27.10) ─────────────────────────────────────────
export type FillerKind = "bumper" | "filler" | "station_id";

export interface FillerItem {
  id: string;
  pool_id: string;
  video_id: string;
  type: FillerKind;
  title?: string | null;
  duration_ms?: number | null;
}

export interface FillerPool {
  id: string;
  channel_id?: string | null;
  name: string;
  items?: FillerItem[];
}

// `filler_collapsed` (27.4 AC10 / 27.6 EC2): filler & bumpers are merged
// in the guide so the grid isn't shredded into slivers; only real program
// blocks and a single "up next" chip survive.
export function isFiller(kind: ProgramKind): boolean {
  return kind === "filler" || kind === "bumper" || kind === "station_id";
}

// progressFraction returns elapsed/total for a program at server-reconciled
// `now`, clamped to [0,1] so a stale `now` never shows a >100% or negative
// bar (27.9 EC2). Returns null when the block isn't currently airing.
export function progressFraction(p: Program, nowMs: number): number | null {
  const start = Date.parse(p.start_at);
  const end = Date.parse(p.end_at);
  if (Number.isNaN(start) || Number.isNaN(end) || end <= start) return null;
  if (nowMs < start || nowMs >= end) return null;
  return Math.min(1, Math.max(0, (nowMs - start) / (end - start)));
}

export function isAiring(p: Program, nowMs: number): boolean {
  const start = Date.parse(p.start_at);
  const end = Date.parse(p.end_at);
  return nowMs >= start && nowMs < end;
}

// ─── API helpers ──────────────────────────────────────────────────────
export const channelsApi = {
  list: (params?: { category?: string; enabled?: boolean }) => {
    const q = new URLSearchParams();
    if (params?.category) q.set("category", params.category);
    if (params?.enabled !== undefined) q.set("enabled", String(params.enabled));
    const qs = q.toString();
    return api.get<{ items: Channel[] }>(`/api/channels${qs ? `?${qs}` : ""}`);
  },
  get: (id: string) => api.get<Channel>(`/api/channels/${encodeURIComponent(id)}`),
  create: (body: Partial<Channel>) => api.post<Channel>("/api/channels", body),
  update: (id: string, body: Partial<Channel>) =>
    api.patch<Channel>(`/api/channels/${encodeURIComponent(id)}`, body),
  remove: (id: string) => api.delete<void>(`/api/channels/${encodeURIComponent(id)}`),
  reorder: (order: Array<{ id: string; number: number }>) =>
    api.post<void>("/api/channels/reorder", order),

  guide: (params: { start: string; end: string; category?: string }) => {
    const q = new URLSearchParams({ start: params.start, end: params.end });
    if (params.category) q.set("category", params.category);
    return api.get<GuideResponse>(`/api/channels/guide?${q.toString()}`);
  },
  channelGuide: (id: string, params: { start: string; end: string }) => {
    const q = new URLSearchParams({ start: params.start, end: params.end });
    return api.get<GuideResponse>(
      `/api/channels/${encodeURIComponent(id)}/guide?${q.toString()}`
    );
  },
  now: () => api.get<NowResponse>("/api/channels/now"),
  schedulePreview: (id: string, hours = 48) =>
    api.get<GuideResponse>(
      `/api/channels/${encodeURIComponent(id)}/schedule/preview?hours=${hours}`
    ),
  regenerate: (id: string) =>
    api.post<void>(`/api/channels/${encodeURIComponent(id)}/schedule/regenerate`),

  tune: (id: string) =>
    api.post<TuneResponse>(`/api/channels/${encodeURIComponent(id)}/tune`),
};

export const fillerApi = {
  pools: (channelId?: string) => {
    const qs = channelId ? `?channel_id=${encodeURIComponent(channelId)}` : "";
    return api.get<{ items: FillerPool[] }>(`/api/filler/pools${qs}`);
  },
  createPool: (body: { name: string; channel_id?: string | null }) =>
    api.post<FillerPool>("/api/filler/pools", body),
  updatePool: (id: string, body: { name?: string; channel_id?: string | null }) =>
    api.patch<FillerPool>(`/api/filler/pools/${encodeURIComponent(id)}`, body),
  deletePool: (id: string) =>
    api.delete<void>(`/api/filler/pools/${encodeURIComponent(id)}`),
  addItems: (poolId: string, items: Array<{ video_id: string; type: FillerKind }>) =>
    api.post<{ items: FillerItem[] }>(
      `/api/filler/pools/${encodeURIComponent(poolId)}/items`,
      { items }
    ),
  deleteItem: (id: string) =>
    api.delete<void>(`/api/filler/items/${encodeURIComponent(id)}`),
};
