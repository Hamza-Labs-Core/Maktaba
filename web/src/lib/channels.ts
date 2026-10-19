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
import { api, uploadWithProgress } from "./api";

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
  /** The effective logo / watermark / loading screen and which were set by hand. */
  art?: ChannelArt;
  /** What is airing now (list/get). */
  now_playing?: NowPlaying | null;
  /** Whether the lineup plan wrote this channel, and whether a person changed it since. */
  plan?: PlanStatus | null;
  /** The end of the generated guide: how far ahead the channel is planned. */
  guide_until?: string | null;
}

export interface NowPlaying {
  title: string;
  start_at: string;
  end_at: string;
  progress: number;
}

export interface PlanStatus {
  owned: boolean;
  edited: boolean;
  edited_at?: string;
}

/** Where a channel's definition comes from: the lineup plan, the plan but changed by hand, or a person. */
export type ChannelOrigin = "plan" | "edited" | "manual";

export function channelOrigin(c: Pick<Channel, "plan">): ChannelOrigin {
  if (!c.plan?.owned) return "manual";
  return c.plan.edited ? "edited" : "plan";
}

/** Hours of guide ahead of `nowMs` (0 when none or in the past). */
export function guideHoursAhead(c: Pick<Channel, "guide_until">, nowMs: number): number {
  if (!c.guide_until) return 0;
  const end = Date.parse(c.guide_until);
  if (Number.isNaN(end)) return 0;
  return Math.max(0, (end - nowMs) / 3_600_000);
}

// ─── Builder sources (GET /api/channels/sources) ──────────────────────
export type SourceKind = "series" | "collection" | "lane" | "video";

export interface SourceHit {
  kind: SourceKind;
  id: string;
  name: string;
  count: number;
  duration_ms: number;
  /** a lane's shows */
  detail?: string[];
}

// ─── Channel art ──────────────────────────────────────────────────────
// Uploaded art wins over the lineup plan's until removed ("back to plan").
export type ArtKind = "logo" | "watermark" | "loading";
export const ART_KINDS: ArtKind[] = ["logo", "watermark", "loading"];
export type WatermarkPosition = "top-right" | "top-left" | "bottom-right" | "bottom-left";
export const WATERMARK_POSITIONS: WatermarkPosition[] = [
  "top-left",
  "top-right",
  "bottom-left",
  "bottom-right",
];
/** Largest image the server takes (bytes). */
export const ART_MAX_BYTES = 5 * 1024 * 1024;
/** What each kind accepts (SVG only for the logo); the server checks the bytes. */
export const ART_ACCEPT: Record<ArtKind, string> = {
  logo: "image/png,image/jpeg,image/webp,image/gif,image/svg+xml",
  watermark: "image/png,image/jpeg,image/webp,image/gif",
  loading: "image/png,image/jpeg,image/webp,image/gif",
};

export interface ArtImage {
  url?: string;
  path?: string;
  overridden: boolean;
}

export interface WatermarkArt extends ArtImage {
  position: WatermarkPosition;
  width_pct: number;
  margin_pct: number;
}

export interface ChannelArt {
  logo: ArtImage;
  watermark: WatermarkArt;
  loading: ArtImage;
  /** The channel's own strip settings (mode_config.look.strip); null: the global ones. */
  strip?: Partial<StripSettings> | null;
}

// ─── Channel strip (Story 27.11) ──────────────────────────────────────
// The news-style bar the streaming engine burns into the picture. The
// global defaults are the rules section tv_strip; a channel overrides any
// of them (PATCH /api/channels/{id}/strip), DELETE goes back to them.
export interface StripClock {
  zone: string;
  label_ar: string;
  label_en: string;
}

export interface StripSettings {
  enabled: boolean;
  position: "bottom" | "top";
  height_pct: number;
  bg_color: string;
  bg_opacity: number;
  /** the rule along the bar and the clock's box */
  accent_color: string;
  /** the accent line along the edge facing the picture (% of the bar height; 0: none) */
  rule_height: number;
  /** the title */
  text_color: string;
  /** the «التالي» line */
  next_color: string;
  /** the clock's label and time and the channel name, on the accent */
  clock_text_color: string;
  /** the page number */
  indicator_color: string;
  font_size_pct: number;
  direction: "auto" | "rtl" | "ltr";
  show_next: boolean;
  next_label_ar: string;
  next_label_en: string;
  filler_label: string;
  overflow: "cycle" | "truncate" | "scroll";
  /** overflow "cycle": each page's base time and the «١/٢» beside a paged line */
  page_seconds: number;
  page_indicator: boolean;
  scroll_speed: number;
  show_mode: "always" | "interval";
  show_seconds: number;
  every_minutes: number;
  show_clock: boolean;
  clocks: StripClock[];
  clock_seconds: number;
  clock_format: "24h" | "12h";
  digits: "auto" | "arabic" | "western";
  show_channel_name: boolean;
}

/** What a channel can set on its own (the font is the host's: global only). */
export type ChannelStrip = Partial<StripSettings>;

export const channelStripApi = {
  save: (id: string, body: ChannelStrip) =>
    api.patch<Channel>(`/api/channels/${encodeURIComponent(id)}/strip`, body),
  reset: (id: string) => api.delete<Channel>(`/api/channels/${encodeURIComponent(id)}/strip`),
};

/** Whether a picked file is one the server will take for kind (by type and size). */
export function artFileProblem(kind: ArtKind, file: File): "type" | "size" | null {
  if (file.size > ART_MAX_BYTES) return "size";
  const types = ART_ACCEPT[kind].split(",");
  if (file.type && !types.includes(file.type)) return "type";
  return null;
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
  /** The channel strip's line (Story 27.11): «series · الحلقة 45 — title». */
  on_air?: string | null;
  /** The reference of a Quran verse the title quotes («البقرة ١٨٣»): the strip's last page. */
  strip_ref?: string | null;
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
  /** the lineup plan's key for a pool the plan writer made */
  external_key?: string | null;
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
  on_air?: string;
  strip_ref?: string;
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
    on_air: b.on_air || null,
    strip_ref: b.strip_ref || null,
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
  // Hands an edited plan channel back to the lineup plan (202).
  followPlan: (id: string) =>
    api.post<{ channel_id: string; status: string; queued: boolean }>(
      `/api/channels/${encodeURIComponent(id)}/follow-plan`
    ),
  // What a slot (or a filler pool) can play, with episode counts.
  sources: (kind: SourceKind, q = "", limit = 25) => {
    const qs = new URLSearchParams({ kind, q, limit: String(limit) });
    return api.get<{ kind: SourceKind; items: SourceHit[] }>(`/api/channels/sources?${qs}`);
  },
};

const artPath = (id: string, kind: ArtKind) =>
  `/api/channels/${encodeURIComponent(id)}/art/${kind}`;

export const channelArtApi = {
  upload: (id: string, kind: ArtKind, file: File, onProgress?: (fraction: number) => void) => {
    const form = new FormData();
    form.append("file", file);
    return uploadWithProgress<Channel>(artPath(id, kind), form, onProgress);
  },
  /** Drops the hand-set art: the channel shows the plan's again. */
  revert: (id: string, kind: ArtKind) => api.delete<Channel>(artPath(id, kind)),
  watermark: (
    id: string,
    body: Partial<Pick<WatermarkArt, "position" | "width_pct" | "margin_pct">>
  ) => api.patch<Channel>(artPath(id, "watermark"), body),
};

/** A tuner Plex knows; ``kind`` says whose it is. */
export interface PlexLiveTuner {
  key: string;
  uri: string;
  device_id: string;
  title: string;
  kind: "maktaba" | "dizquetv" | "other";
  in_dvr?: boolean;
}

export interface PlexLiveDvr {
  key: string;
  title: string;
  lineup: string;
  maktaba: boolean;
  devices: PlexLiveTuner[];
}

/** What the plex_livetv stage last saw and did (plex_livetv_state.status). */
export interface PlexLiveReport {
  reachable?: boolean;
  server?: string;
  error?: string | null;
  target?: { tuner_url: string; xmltv_url: string; source: string };
  device?: { device_id: string; name: string; enabled: boolean };
  dvrs?: PlexLiveDvr[];
  tuners?: PlexLiveTuner[];
  maktaba?: { tuner_key: string | null; dvr_key: string | null; registered: boolean };
  dizquetv_present?: boolean;
  channels?: { lineup: number; mapped: number | null; in_sync: boolean | null };
  actions?: { register: string; channel_map: string; guide: string };
  warnings?: string[];
}

/** GET /api/tv/plex */
export interface PlexLiveStatus {
  plex: { enabled: boolean; configured: boolean; url: string };
  stage: { enabled: boolean; disabled_reason: string | null } | null;
  job: {
    id: number;
    state: string;
    created_at: string | null;
    finished_at: string | null;
    paused_reason: string | null;
  } | null;
  checked_at: string | null;
  guide_reloaded_at: string | null;
  channelmap_at: string | null;
  last_error: string | null;
  last_error_at: string | null;
  register_requested_at: string | null;
  status: PlexLiveReport;
}

export interface PlexLiveAction {
  queued: boolean;
  outcome: string;
  job_id?: number;
}

export const tvApi = {
  info: () => api.get<TvInfo>("/api/tv/info"),
  hdhr: () => api.get<HdhrDevice>("/api/tv/hdhr"),
  updateHdhr: (body: Partial<Pick<HdhrDevice, "enabled" | "friendly_name" | "tuner_count">>) =>
    api.patch<HdhrDevice>("/api/tv/hdhr", body),
  /** Plex Live TV: what the plex_livetv stage last saw (admin). */
  plex: () => api.get<PlexLiveStatus>("/api/tv/plex"),
  /** Queue plex_livetv now: push the channel map and reload the guide. */
  plexRefresh: () => api.post<PlexLiveAction>("/api/tv/plex/refresh"),
  /** Add Maktaba's tuner and a DVR with its XMLTV guide to Plex, once (409 when present). */
  plexRegister: () => api.post<PlexLiveAction>("/api/tv/plex/register"),
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
