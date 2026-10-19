// Epic 32 story 32.13: simulcast a channel to YouTube, Facebook or any
// RTMP/RTMPS destination through Cloudflare live outputs (admin).
//   GET    /api/tv/simulcast                               SimulcastView
//   POST   /api/tv/simulcast/destinations                  {name, kind, ingest_url?, stream_key}
//   PUT    /api/tv/simulcast/destinations/{id}             {name?, ingest_url?, stream_key?}
//   DELETE /api/tv/simulcast/destinations/{id}             409 destination_in_use
//   POST   /api/tv/simulcast/destinations/{id}/test        DestinationTest
//   GET    /api/channels/{id}/simulcast                    SimulcastView (that channel's attachments)
//   PUT    /api/channels/{id}/simulcast                    AttachmentInput[] -> 409 rights_confirmation_required
//   POST   /api/tv/simulcast/outputs/{attachment}/start|stop
// A destination's stream key is stored on the home, encrypted, and only ever
// answered masked.
import { ApiError, api } from "./api";

export type DestinationKind = "youtube" | "facebook" | "rtmp";
export type AttachmentMode = "always" | "schedule";
export type OutputState = "scheduled" | "starting" | "live" | "stopping" | "off" | "errored";

export interface Destination {
  id: string;
  name: string;
  kind: DestinationKind;
  ingest_url: string;
  key_set: boolean;
  stream_key_masked?: string;
  in_use: boolean;
}

export interface ScheduleRule {
  kind: "window" | "programme" | "series" | "live";
  days?: number[];
  start?: string;
  end?: string;
  video_id?: string;
  series_id?: string;
}

export interface SimWindow {
  start: string;
  end: string;
}

export interface OutputView {
  state: OutputState;
  detail?: string;
  error?: string;
  cf_status?: string;
  platform_note: boolean;
  started_at?: string;
  window_end?: string;
  next_window?: SimWindow;
  push_state?: string;
  input_error?: string;
  minutes: number;
}

export interface AttachmentView {
  id: string;
  channel_id: string;
  destination_id: string;
  mode: AttachmentMode;
  rules: ScheduleRule[];
  enabled: boolean;
  manual?: "start" | "stop";
  manual_until?: string;
  rights_confirmed_by?: string;
  rights_confirmed_at?: string;
  channel_name: string;
  channel_number: number;
  destination_name: string;
  destination_kind: DestinationKind;
  output: OutputView;
  windows?: SimWindow[];
}

export interface SimulcastUsage {
  month: string;
  minutes: number;
  hours: number;
  cap_hours: number;
  percent: number;
  capped: boolean;
  cost_usd: number;
  budget_exhausted: boolean;
}

export interface SimulcastView {
  enabled: boolean;
  ready: boolean;
  reason?: string;
  settings: {
    max_outputs_per_channel: number;
    max_hours_per_month: number;
    lead_seconds: number;
    tail_seconds: number;
  };
  destinations: Destination[];
  attachments: AttachmentView[];
  usage: SimulcastUsage;
  /** The highest cap threshold (80 / 100 %) reached this month (32.13 AC9). */
  notice?: { month: string; level: number; at?: string; hours?: number; cap_hours?: number } | null;
  default_ingest: Partial<Record<DestinationKind, string>>;
}

export interface DestinationTest {
  ok: boolean;
  url_ok: boolean;
  key_set: boolean;
  detail: string;
}

export interface AttachmentInput {
  destination_id: string;
  mode: AttachmentMode;
  rules: ScheduleRule[];
  enabled: boolean;
  rights_confirmed?: boolean;
}

export interface DestinationInput {
  name?: string;
  kind?: DestinationKind;
  ingest_url?: string;
  stream_key?: string;
}

const enc = encodeURIComponent;

export const tvSimulcastApi = {
  status: () => api.get<SimulcastView>("/api/tv/simulcast"),
  channel: (id: string) => api.get<SimulcastView>(`/api/channels/${enc(id)}/simulcast`),
  setChannel: (id: string, body: AttachmentInput[]) =>
    api.put<SimulcastView>(`/api/channels/${enc(id)}/simulcast`, body),
  createDestination: (body: DestinationInput) =>
    api.post<Destination>("/api/tv/simulcast/destinations", body),
  updateDestination: (id: string, body: DestinationInput) =>
    api.put<Destination>(`/api/tv/simulcast/destinations/${enc(id)}`, body),
  deleteDestination: (id: string) => api.delete<void>(`/api/tv/simulcast/destinations/${enc(id)}`),
  testDestination: (id: string) =>
    api.post<DestinationTest>(`/api/tv/simulcast/destinations/${enc(id)}/test`),
  start: (attachment: string) =>
    api.post<void>(`/api/tv/simulcast/outputs/${enc(attachment)}/start`),
  stop: (attachment: string) => api.post<void>(`/api/tv/simulcast/outputs/${enc(attachment)}/stop`),
};

/** The stable code of a refused call (rights_confirmation_required, …), if any. */
export function simulcastErrCode(e: unknown): string | undefined {
  if (e instanceof ApiError) {
    const c = (e.problem as unknown as { code?: unknown }).code;
    return typeof c === "string" ? c : undefined;
  }
  return undefined;
}
