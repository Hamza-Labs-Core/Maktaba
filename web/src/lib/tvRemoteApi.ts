// Epic 32 story 32.6: the Cloudflare Stream integration (remote viewing).
//   GET    /api/tv/remote              { integration: RemoteIntegration }
//   POST   /api/tv/remote/test         RemoteTestResult
//   PUT    /api/tv/remote/enabled      { enabled } -> { integration } | 409 (needs a green test < 24 h)
//   DELETE /api/tv/remote/integration  { confirm: true } -> RemoteRemoveResult
//   POST   /api/tv/remote/signing-key/rotate -> RotateResult (every remote token stops)
//   POST   /api/tv/remote/pushes/{id}/stop
// The credentials are saved through the extensions API (cloudflare_stream);
// the API token is stored on the home, encrypted, and never returned.
import { api } from "./api";

export type RemoteLineKey =
  | "token"
  | "stream"
  | "storage"
  | "analytics"
  | "signing_key"
  | "customer_domain"
  | "push";

export interface RemoteTestLine {
  key: RemoteLineKey | string;
  ok: boolean;
  optional?: boolean;
  skipped?: boolean;
  detail: string;
  /** Why a check failed (extension tests: timeout, token_rejected, livetv_missing, ...). */
  code?: string;
  /** The check's round trip (extension tests). */
  latency_ms?: number;
}

export interface RemoteTestResult {
  ok: boolean;
  message: string;
  checked_at: string;
  lines: RemoteTestLine[];
}

export interface RemoteIntegration {
  configured: boolean;
  account_id: string;
  token_set: boolean;
  token_problem: string | null;
  signing_key_set: boolean;
  customer_domain: string | null;
  enabled: boolean;
  can_enable: boolean;
  last_test: RemoteTestResult | null;
}

// Stories 32.7-32.10: the remote controller's part of GET /api/tv/remote.
export interface RemoteEstimate {
  month: string;
  delivered_minutes: number;
  running_minutes: number;
  delivery_usd: number;
  storage_usd: number;
  total_usd: number;
  budget_usd: number;
  percent: number;
  action: "block_new" | "warn" | string;
  source: "analytics" | "sessions" | string;
  peak_recording_minutes: number;
  exhausted: boolean;
}

export interface RemoteStorage {
  capacity_minutes: number;
  capacity_source: "cloudflare" | "settings" | string;
  stored_minutes: number;
  other_minutes: number;
  running_reserved_minutes: number;
  pending_recording_minutes: number;
  running_pushes: number;
  need_minutes: number;
  margin_minutes: number;
  free_minutes: number;
  fits: boolean;
  hint?: string;
}

export interface RemotePush {
  id: string;
  channel_id: string;
  state: string;
  host_id?: string;
  video_uid?: string;
  started_at: string;
  live_at?: string;
  end_reason?: string;
  detail?: string;
  relay_state?: string;
  viewers: number;
  /** Cloudflare's live viewer count (null: hidden or unavailable, story 32.11 EC1). */
  cf_viewers?: number | null;
  channel_name?: string;
  channel_number?: number;
  ended_at?: string;
}

/** An open remote session (story 32.11 AC4): a user name and a device, never an address. */
export interface RemoteSessionView {
  id: string;
  user: string;
  device: string;
  channel_id: string;
  channel_name?: string;
  channel_number?: number;
  push_id?: string;
  started_at: string;
  last_beat_at: string;
}

/** A remote event from the API's log ring (story 32.11 AC4). */
export interface RemoteEvent {
  at: string;
  level: string;
  event: string;
  message: string;
  channel_id?: string;
  push_id?: string;
  host_id?: string;
  code?: string;
}

export interface RemoteInput {
  id: string;
  cf_uid: string;
  name: string;
  state: string;
  channel_id?: string;
  assigned_at?: string;
  error?: string;
  error_at?: string;
}

export interface RemoteControllerStatus {
  ready: boolean;
  reason?: string;
  unavailable?: string;
  inputs: RemoteInput[];
  pushes: RemotePush[];
  sessions: number;
  session_list?: RemoteSessionView[];
  events?: RemoteEvent[];
  estimate: RemoteEstimate;
  storage: RemoteStorage;
  limits: {
    channels_in_use: number;
    max_channels: number;
    channels_line: string;
    stored_line: string;
    max_streams_per_user: number;
    upload_mbps: number;
  };
  /** The highest budget threshold (80 / 100 %) reached this month (32.10 AC4). */
  budget_notice?: {
    month: string;
    level: number;
    at?: string;
    total_usd?: number;
    budget_usd?: number;
    action?: string;
  } | null;
}

export interface RotateResult {
  key_id: string;
  old_key_id?: string;
  old_deleted: boolean;
  error?: string;
}

export interface RemoteRemoveResult {
  deleted_inputs: string[];
  deleted_signing_key?: string;
  errors: string[];
}

export const tvRemoteApi = {
  status: () =>
    api.get<{ integration: RemoteIntegration; remote?: RemoteControllerStatus }>("/api/tv/remote"),
  rotateKey: () => api.post<RotateResult>("/api/tv/remote/signing-key/rotate"),
  stopPush: (id: string) => api.post<void>(`/api/tv/remote/pushes/${encodeURIComponent(id)}/stop`),
  test: () => api.post<RemoteTestResult>("/api/tv/remote/test"),
  setEnabled: (enabled: boolean) =>
    api.put<{ integration: RemoteIntegration }>("/api/tv/remote/enabled", { enabled }),
  remove: () => api.delete<RemoteRemoveResult>("/api/tv/remote/integration", { confirm: true }),
};
