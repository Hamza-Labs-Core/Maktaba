// Epic 45 (story 45.5): the server's storage and discovery settings, which live
// in server.toml. The server (its supervisor) owns the file; the API passes these
// through, admin-only. Every change applies at the next server restart.
//
//   GET  /api/server/settings   { available: true, file, saved, running, restart_needed,
//                                 can_restart, reachability }, or without a supervisor
//                                 { available: false, reason }
//   PUT  /api/server/settings   partial ServerSettings -> the GET document
//                               (422 {errors: [{field, message}]}, field as
//                               "server.listen", "media.path_map[0].local", "media.roots[1]")
//   POST /api/server/restart    202 {restarting: true}; 409 when the server cannot
//                               restart itself
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

/** A library folder, where it is on this machine under the saved map, and whether it reads. */
export interface ReachabilityRow {
  library_id: string;
  library_name: string;
  root: string;
  local: string;
  reachable: boolean;
  problem?: string;
}

export type ServerSettingsDoc =
  | { available: false; reason: string }
  | {
      available: true;
      file: string;
      saved: ServerSettings;
      running: ServerSettings;
      restart_needed: boolean;
      can_restart: boolean;
      reachability: ReachabilityRow[];
    };

/** The addresses and public URLs, in the order the page shows them. */
export const ADDRESS_KEYS = ["listen", "web_listen", "stream_addr"] as const;
export const URL_KEYS = ["public_url", "web_public_url"] as const;

export const serverSettingsApi = {
  get: async () => api.get<ServerSettingsDoc>("/api/server/settings"),
  save: async (
    body: Partial<{
      media: Partial<ServerSettings["media"]>;
      server: Partial<ServerSettings["server"]>;
    }>
  ) => api.put<ServerSettingsDoc>("/api/server/settings", body),
  restart: async () => api.post<{ restarting: boolean }>("/api/server/restart"),
  /** Answers while the server is up (public; used to see it come back). */
  ping: async () => api.get<unknown>("/api/system/version"),
};
