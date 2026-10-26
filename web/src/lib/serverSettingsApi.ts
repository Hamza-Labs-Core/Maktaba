// Epic 45 (story 45.5): the server's storage and discovery settings, which live
// in server.toml. The server (its supervisor) owns the file; the API passes these
// through, admin-only. Every change applies at the next server restart.
//
//   GET  /api/server/settings   { available, file, saved, running, restart_needed,
//                                 can_restart, reachability }  ({available: false}
//                                 without a supervisor)
//   PUT  /api/server/settings   partial ServerSettings -> the GET document
//                               (422 {errors: [{field, message}]})
//   POST /api/server/restart    409 when the server cannot restart itself
import { api } from "./api";

export interface PathMapPair {
  db: string;
  local: string;
}

export interface ServerSettings {
  media: { path_map: PathMapPair[]; roots: string[] };
  server: {
    mdns: boolean;
    listen: string;
    web_listen: string;
    stream_addr: string;
    public_url: string;
    web_public_url: string;
  };
}

export interface ReachabilityRow {
  library_id?: string;
  library_name?: string;
  path: string;
  local_path?: string;
  reachable: boolean;
  problem?: string | null;
}

export interface ServerSettingsDoc {
  available: boolean;
  file?: string;
  saved?: ServerSettings;
  running?: ServerSettings;
  restart_needed?: boolean;
  can_restart?: boolean;
  reachability?: ReachabilityRow[];
}

/** The four addresses and two public URLs, in the order the page shows them. */
export const ADDRESS_KEYS = ["listen", "web_listen", "stream_addr"] as const;
export const URL_KEYS = ["public_url", "web_public_url"] as const;

/** A settings document with every list and string present. */
export function normaliseSettings(s: Partial<ServerSettings> | undefined): ServerSettings {
  const m: Partial<ServerSettings["media"]> = s?.media ?? {};
  const v: Partial<ServerSettings["server"]> = s?.server ?? {};
  return {
    media: {
      path_map: (m.path_map ?? []).map((p) => ({ db: p?.db ?? "", local: p?.local ?? "" })),
      roots: m.roots ?? [],
    },
    server: {
      mdns: !!v.mdns,
      listen: v.listen ?? "",
      web_listen: v.web_listen ?? "",
      stream_addr: v.stream_addr ?? "",
      public_url: v.public_url ?? "",
      web_public_url: v.web_public_url ?? "",
    },
  };
}

export const serverSettingsApi = {
  get: async () => api.get<ServerSettingsDoc>("/api/server/settings"),
  save: async (
    body: Partial<{
      media: Partial<ServerSettings["media"]>;
      server: Partial<ServerSettings["server"]>;
    }>
  ) => api.put<ServerSettingsDoc>("/api/server/settings", body),
  restart: async () => api.post<unknown>("/api/server/restart"),
  /** Answers while the server is up (public; used to see it come back). */
  ping: async () => api.get<unknown>("/api/system/version"),
};
