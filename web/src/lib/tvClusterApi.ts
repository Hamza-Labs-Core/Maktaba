// Typed client for the TV streaming cluster (Epic 32, story 32.5): which
// machines stream TV, how full they are, and which channel is on air where.
//
//   GET    /api/tv/cluster                              the whole page (admin)
//   PUT    /api/tv/cluster/hosts/{host_id}              switch / drain / tune a host
//   POST   /api/tv/cluster/channels/{id}/move           {host_id?} (none = automatic) → 202
//   DELETE /api/tv/cluster/channels/{id}/placement      stop a channel → 204
//   PATCH  /api/rules/tv_cluster                        the cluster settings and pins
import { api } from "./api";
import { rulesApi, rulesError } from "./rulesApi";

export type ClusterPolicy = "home_first" | "balanced";

export interface TvClusterSettings {
  nodes_stream: boolean;
  policy: ClusterPolicy;
  spill_at: number;
  hls_mode: "proxy" | "redirect";
  failover: boolean;
  dead_after_seconds: number;
  /** channel slug → [host name] (one host); absent or empty = automatic */
  pins: Record<string, string[]>;
}

export type HostState = "online" | "down" | "draining" | "disabled" | "unfit";

export interface StreamingHost {
  host_id: string;
  role: "home" | "node";
  name: string;
  state: HostState;
  /** why it is not taking channels */
  reason: string | null;
  enabled: boolean;
  draining: boolean;
  encoders: string[];
  capacity: number;
  capacity_override: number | null;
  used: number;
  weight: number;
  load: { cpu?: number; gpu_sessions?: number; nvenc_max?: number };
  can_push: { rtmps?: boolean; srt?: boolean };
  can_live: boolean;
  version: string;
  last_beat_at: string | null;
  stream_base_url: string;
  stream_base_url_source: "reported" | "admin";
  /** the channel ids placed on this host */
  channels: string[];
}

export type OnAirState = "placing" | "live" | "moving" | "runtime";

export interface OnAirChannel {
  channel_id: string;
  number: number;
  name: string;
  slug: string;
  host_id: string;
  host_name: string;
  generation: number;
  state: OnAirState;
  reason: string;
  since: string | null;
  encoder: string;
  viewers: { hls: number; tuners: number; remote: number };
  restarts: number;
  nvenc_fallbacks: number;
  push: null | { state: string };
  stale: boolean;
}

export interface ClusterNotice {
  kind: "two_homes" | "no_room" | "pin_skipped";
  message: string;
  hosts?: string[];
  channel_id?: string;
  at?: string;
}

export interface TvClusterView {
  settings: TvClusterSettings;
  hosts: StreamingHost[];
  on_air: OnAirChannel[];
  notices: ClusterNotice[];
  /** Channels placed by a failover in the last 24 h (story 32.11 AC5). */
  failovers_24h?: number;
}

export interface HostUpdate {
  enabled?: boolean;
  drain?: boolean;
  move_now?: boolean;
  /** 0..64; null = automatic */
  capacity_override?: number | null;
  /** 1..100 */
  weight?: number;
  /** http(s) URL; null or "" = back to the one the host reports */
  stream_base_url?: string | null;
}

export interface MoveResult {
  channel_id: string;
  host_id: string;
  generation: number;
}

const q = encodeURIComponent;

export const tvClusterApi = {
  /**
   * Lists the API leaves out (or an older API sends as null) read as empty,
   * and so does a whole `null` body.
   */
  get: () =>
    api.get<TvClusterView | null>("/api/tv/cluster").then((raw) => {
      const v = raw ?? ({} as Partial<TvClusterView>);
      return {
        ...v,
        hosts: v.hosts ?? [],
        on_air: v.on_air ?? [],
        notices: v.notices ?? [],
      } as TvClusterView;
    }),
  updateHost: (hostId: string, body: HostUpdate) =>
    api.put<StreamingHost>(`/api/tv/cluster/hosts/${q(hostId)}`, body),
  /** hostId omitted: the cluster picks the host */
  move: (channelId: string, hostId?: string) =>
    api.post<MoveResult>(
      `/api/tv/cluster/channels/${q(channelId)}/move`,
      hostId ? { host_id: hostId } : {}
    ),
  stop: (channelId: string) =>
    api.delete<void>(`/api/tv/cluster/channels/${q(channelId)}/placement`),
  /** A cluster setting (rules section tv_cluster). */
  setSetting: (name: "nodes_stream" | "policy" | "spill_at", value: unknown) =>
    rulesApi.patch("tv_cluster", { [name]: value }, []),
  /** Pins `slug` to `host` (null = automatic), keeping every other pin. */
  setPin: (pins: Record<string, string[]>, slug: string, host: string | null) => {
    const next: Record<string, string[]> = { ...pins };
    if (host) next[slug] = [host];
    else delete next[slug];
    return rulesApi.patch("tv_cluster", { pins: next }, []);
  },
};

/** The hosts a channel can be moved to: online and not draining. */
export function movableHosts(hosts: StreamingHost[]): StreamingHost[] {
  return hosts.filter((h) => h.state === "online" && !h.draining);
}

/** The message of a failed cluster call ({error} bodies included). */
export const clusterErrText = (e: unknown): string => rulesError(e).message;
