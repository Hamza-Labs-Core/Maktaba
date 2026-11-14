// Epic 50 story 50.22: home broadcasting, admin only (Admin → Live TV → Broadcast).
//
//   GET  /api/tv/broadcast                          BroadcastView | 409 component_off (livetv.broadcast)
//   PUT  /api/tv/broadcast/channels/{id}            BroadcastSetup -> BroadcastChannel
//   POST /api/tv/broadcast/channels/{id}/publish    {confirm_rights} -> BroadcastChannel | 409 broadcast.<code>
//   POST /api/tv/broadcast/channels/{id}/unpublish  -> BroadcastChannel
//   POST /api/tv/broadcast/speedtest                -> Uplink
import { api, ApiError } from "./api";

export type BroadcastState = "draft" | "live" | "offline" | "off_air" | "revoked";

export interface BroadcastChannel {
  id: string;
  name: string;
  number: number;
  enabled: boolean;
  configured: boolean;
  slug: string;
  access: "invite" | "open";
  mode: "always_on" | "on_demand";
  rungs: string[];
  countries: string[];
  state: BroadcastState;
  edge_channel?: string;
  dropped_rungs: number;
  detail?: string;
  published_at?: string;
  published_by?: string;
}

export interface Uplink {
  kbps: number;
  measured_at?: string;
}

export interface BroadcastPlan {
  plan: string;
  max_channels: number;
  max_rungs: number;
  max_top_kbps: number;
  access: string[];
  on_demand_only: boolean;
  agreement_accepted: boolean;
}

export interface BroadcastView {
  channels: BroadcastChannel[];
  uplink: Uplink;
  plan: BroadcastPlan | null;
  cloud: "ok" | "unreachable" | "unclaimed";
  settings: { uplink_share: number; rung_slot_weight: number; allow_software_rungs: boolean };
  uplink_budget_kbps: number;
  uplink_used_kbps: number;
  rungs: string[];
  source_height: number;
  checks: string[];
}

export interface BroadcastSetup {
  slug: string;
  access: "invite" | "open";
  mode: "always_on" | "on_demand";
  rungs: string[];
  countries: string[];
}

/** A refusal: its i18n key (broadcast.<code>), the check that failed and its values. */
export interface BroadcastRefusal {
  code: string;
  check?: string;
  params: Record<string, string | number>;
}

export function broadcastRefusal(e: unknown): BroadcastRefusal | null {
  if (!(e instanceof ApiError)) return null;
  const p = e.problem as unknown as {
    code?: string;
    check?: string;
    params?: Record<string, unknown>;
  };
  if (!p.code || !p.code.startsWith("broadcast.")) return null;
  const params: Record<string, string | number> = {};
  for (const [k, v] of Object.entries(p.params ?? {})) {
    if (typeof v === "string" || typeof v === "number") params[k] = v;
  }
  return { code: p.code, check: p.check, params };
}

/** The public link of a live channel (the watch site, story 50.26). */
export function watchLink(slug: string): string {
  return `https://watch.maktaba.tv/${slug}`;
}

const ch = (id: string) => `/api/tv/broadcast/channels/${encodeURIComponent(id)}`;

export const tvBroadcastApi = {
  view: () => api.get<BroadcastView>("/api/tv/broadcast"),
  setup: (id: string, s: BroadcastSetup) => api.put<BroadcastChannel>(ch(id), s),
  publish: (id: string, confirmRights: boolean) =>
    api.post<BroadcastChannel>(`${ch(id)}/publish`, { confirm_rights: confirmRights }),
  unpublish: (id: string) => api.post<BroadcastChannel>(`${ch(id)}/unpublish`),
  speedtest: () => api.post<Uplink>("/api/tv/broadcast/speedtest"),
};
