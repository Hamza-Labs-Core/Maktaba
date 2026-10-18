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
  /** Servable logo (GET /api/channels/{id}/logo); set whenever logo_path is. */
  logo_url?: string | null;
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

// ─── Live TV access (/api/tv) ─────────────────────────────────────────
// Where the TV streams live: the streaming service's LAN address (what
// Plex is pointed at) plus the same-origin paths the web port proxies.
export interface TvInfo {
  stream_base_url: string;
  hdhr_address: string;
  xmltv_url: string;
  m3u_url: string;
  base_from_config: boolean;
  xmltv_path: string;
  m3u_path: string;
  hls_path_template: string;
}

// The emulated HDHomeRun tuner Plex / Jellyfin / Emby add as a DVR.
export interface HdhrDevice {
  device_id: string;
  friendly_name: string;
  tuner_count: number;
  enabled: boolean;
  max_tuners: number;
}

/**
 * The channel's live HLS playlist, through the web port (it proxies
 * /stream/ to the streaming service). Tuning is lazy: requesting this
 * URL starts the channel, so there is no separate "tune" call.
 */
export function liveStreamUrl(channelId: string): string {
  return `/stream/channel/${encodeURIComponent(channelId)}/index.m3u8`;
}

/** The logo the browser should load for a channel, if it has one. */
export function channelLogo(c: Pick<Channel, "logo_url" | "logo_path">): string | null {
  if (c.logo_url) return c.logo_url;
  // Older payloads only carried the raw value; a URL is still loadable.
  if (c.logo_path && /^https?:\/\//i.test(c.logo_path)) return c.logo_path;
  return null;
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

// ─── Wire format ──────────────────────────────────────────────────────
// The Go guide handlers (api/internal/handlers/guide) speak the XMLTV-ish
// `Block` shape — `start`/`stop`/`desc`/`poster`, `blocks` per channel,
// `channels` for now/next — and a channel header without mode/enabled.
// They are mapped here, once, onto the client shapes above.
interface WireBlock {
  channel_id: string;
  kind: string;
  start: string;
  stop: string;
  title: string;
  sub_title?: string;
  desc?: string;
  poster?: string;
  series?: string;
  season?: number;
  episode?: number;
}

interface WireHeader {
  id: string;
  number: number;
  name: string;
  slug?: string;
  logo?: string;
  logo_url?: string;
}

function fromHeader(h: WireHeader): Channel {
  return {
    id: h.id,
    number: h.number,
    name: h.name,
    slug: h.slug,
    logo_path: h.logo ?? null,
    logo_url: h.logo_url ?? null,
    mode: "schedule",
    enabled: true,
    sort_order: h.number,
  };
}

function episodeLabel(b: WireBlock): string | null {
  if (b.episode == null) return b.sub_title ?? null;
  const se = b.season != null ? `S${b.season}E${b.episode}` : `E${b.episode}`;
  return b.sub_title ? `${se} · ${b.sub_title}` : se;
}

function fromBlock(b: WireBlock, seq: number): Program {
  return {
    channel_id: b.channel_id,
    seq,
    kind: (b.kind as ProgramKind) || "program",
    title: b.title,
    start_at: b.start,
    end_at: b.stop,
    poster_url: b.poster || null,
    description: b.desc || null,
    series: b.series || null,
    episode: episodeLabel(b),
  };
}

function serverTime(t?: string): string {
  return t ?? new Date().toISOString();
}

async function guideFrom(
  req: Promise<{
    server_time?: string;
    channels?: Array<{ channel: WireHeader; blocks?: WireBlock[] }>;
  }>
): Promise<GuideResponse> {
  const r = await req;
  return {
    server_time: serverTime(r.server_time),
    channels: (r.channels ?? []).map((c) => ({
      channel: fromHeader(c.channel),
      programs: (c.blocks ?? []).map(fromBlock),
    })),
  };
}

async function nowFrom(
  req: Promise<{
    server_time?: string;
    channels?: Array<{ channel: WireHeader; current?: WireBlock | null; next?: WireBlock | null }>;
  }>
): Promise<NowResponse> {
  const r = await req;
  return {
    server_time: serverTime(r.server_time),
    items: (r.channels ?? []).map((e) => ({
      channel: fromHeader(e.channel),
      current: e.current ? fromBlock(e.current, 0) : null,
      next: e.next ? fromBlock(e.next, 1) : null,
    })),
  };
}

// The API decodes channel bodies strictly (unknown fields are a 400), so
// only the writable fields are sent — never id/slug/timestamps/now_playing
// or the derived logo_url an edit form carries along.
const PATCH_FIELDS = [
  "number",
  "name",
  "logo_path",
  "category",
  "mode",
  "mode_config",
  "source_filter",
  "transition",
  "enabled",
  "sort_order",
] as const;
const CREATE_FIELDS = [...PATCH_FIELDS, "library_id"] as const;

export function channelBody(
  c: Partial<Channel>,
  fields: readonly (keyof Channel)[] = PATCH_FIELDS
): Partial<Channel> {
  const out: Record<string, unknown> = {};
  for (const f of fields) {
    const v = c[f];
    if (v !== undefined && v !== null) out[f] = v;
  }
  // An emptied logo field clears the logo (the API maps "" to NULL).
  if (fields.includes("logo_path") && c.logo_path === null) out.logo_path = "";
  return out as Partial<Channel>;
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
  create: (body: Partial<Channel>) =>
    api.post<Channel>("/api/channels", channelBody(body, CREATE_FIELDS)),
  update: (id: string, body: Partial<Channel>) =>
    api.patch<Channel>(`/api/channels/${encodeURIComponent(id)}`, channelBody(body, PATCH_FIELDS)),
  remove: (id: string) => api.delete<void>(`/api/channels/${encodeURIComponent(id)}`),
  reorder: (order: Array<{ id: string; number: number }>) =>
    api.post<void>("/api/channels/reorder", order),

  guide: (params: { start: string; end: string; category?: string }) => {
    const q = new URLSearchParams({ start: params.start, end: params.end });
    if (params.category) q.set("category", params.category);
    return guideFrom(api.get(`/api/channels/guide?${q.toString()}`));
  },
  channelGuide: (id: string, params: { start: string; end: string }) => {
    const q = new URLSearchParams({ start: params.start, end: params.end });
    // The per-channel guide answers one `{channel, blocks}`, not a list.
    return guideFrom(
      api
        .get<{
          server_time?: string;
          channel: WireHeader;
          blocks?: WireBlock[];
        }>(`/api/channels/${encodeURIComponent(id)}/guide?${q.toString()}`)
        .then((r) => ({ server_time: r.server_time, channels: [r] }))
    );
  },
  now: () => nowFrom(api.get("/api/channels/now")),
  // The next `hours` of a channel's generated schedule, read from its
  // per-channel guide (there is no separate preview endpoint).
  schedulePreview: (id: string, hours = 48, now: Date = new Date()): Promise<GuideResponse> =>
    channelsApi.channelGuide(id, {
      start: now.toISOString(),
      end: new Date(now.getTime() + hours * 3_600_000).toISOString(),
    }),
  // Marks the schedule stale; the scheduler rebuilds the future tail (202).
  regenerate: (id: string) =>
    api.post<{ channel_id: string; status: string }>(
      `/api/channels/${encodeURIComponent(id)}/regenerate`
    ),
};

export const tvApi = {
  info: () => api.get<TvInfo>("/api/tv/info"),
  hdhr: () => api.get<HdhrDevice>("/api/tv/hdhr"),
  updateHdhr: (body: Partial<Pick<HdhrDevice, "enabled" | "friendly_name" | "tuner_count">>) =>
    api.patch<HdhrDevice>("/api/tv/hdhr", body),
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
  deletePool: (id: string) => api.delete<void>(`/api/filler/pools/${encodeURIComponent(id)}`),
  addItems: (poolId: string, items: Array<{ video_id: string; type: FillerKind }>) =>
    api.post<{ items: FillerItem[] }>(`/api/filler/pools/${encodeURIComponent(poolId)}/items`, {
      items,
    }),
  deleteItem: (id: string) => api.delete<void>(`/api/filler/items/${encodeURIComponent(id)}`),
};
