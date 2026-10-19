// Epic 32 story 32.6: the Cloudflare Stream integration (remote viewing).
//   GET    /api/tv/remote              { integration: RemoteIntegration }
//   POST   /api/tv/remote/test         RemoteTestResult
//   PUT    /api/tv/remote/enabled      { enabled } -> { integration } | 409 (needs a green test < 24 h)
//   DELETE /api/tv/remote/integration  { confirm: true } -> RemoteRemoveResult
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

export interface RemoteRemoveResult {
  deleted_inputs: string[];
  deleted_signing_key?: string;
  errors: string[];
}

export const tvRemoteApi = {
  status: () => api.get<{ integration: RemoteIntegration }>("/api/tv/remote"),
  test: () => api.post<RemoteTestResult>("/api/tv/remote/test"),
  setEnabled: (enabled: boolean) =>
    api.put<{ integration: RemoteIntegration }>("/api/tv/remote/enabled", { enabled }),
  remove: () => api.delete<RemoteRemoveResult>("/api/tv/remote/integration", { confirm: true }),
};
