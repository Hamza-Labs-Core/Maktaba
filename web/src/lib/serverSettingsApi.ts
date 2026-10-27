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
//
// Where Maktaba keeps its data (its database, backups, models, caches), and
// moving it to another disk. The move stops the server while it copies:
//
//   GET  /api/server/storage            StorageDoc
//   GET  /api/server/storage/disks      {disks}: the disks a move could go to
//   POST /api/server/storage/check      {target} -> MoveCheck
//   POST /api/server/storage/move       {target} -> 202 {state: "requested", target};
//                                       422 the MoveCheck (ok: false); 409 no supervisor
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

/** A finding about a move (storage.location.note.<code>), its English as the fallback. */
export interface StorageNote {
  code: string;
  message: string;
  params?: Record<string, unknown>;
}

/** A mounted disk: one Maktaba's folders are on, or one a move could go to. */
export interface StorageDisk {
  mount: string;
  name: string;
  free_bytes: number;
  total_bytes: number;
  fs_type: string;
  local: boolean;
  /** a move can go there (problem says why not) */
  suitable: boolean;
  problem?: string;
  /** the folder a move there would use */
  suggested_dir: string;
  internal: boolean;
  /** the disk the data is on now */
  current: boolean;
}

/** One of the folders Maktaba keeps its data in. */
export interface StorageFolder {
  name: string;
  path: string;
  bytes: number;
  files: number;
  exists: boolean;
  /** the mount point of its disk */
  disk: string;
}

export type MoveState = "none" | "requested" | "moving" | "failed" | "done";

export interface StorageMove {
  state: MoveState;
  target?: string;
  error?: string;
  /** RFC 3339 */
  at?: string;
  /** the old copies kept after a move, for the owner to delete */
  old?: string[];
  done_bytes?: number;
  total_bytes?: number;
}

export type StorageDoc =
  | { available: false; reason: string }
  | {
      available: true;
      data_dir: string;
      state_dir: string;
      default_data_dir: string;
      total_bytes: number;
      folders: StorageFolder[];
      disks: StorageDisk[];
      move: StorageMove;
      can_move: boolean;
      reason?: string;
    };

/** What moving to a folder would do: problems stop it, warnings do not. */
export interface MoveCheck {
  ok: boolean;
  target: string;
  need_bytes: number;
  free_bytes: number;
  problems: StorageNote[];
  warnings: StorageNote[];
}

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
  storage: async () => api.get<StorageDoc>("/api/server/storage"),
  disks: async () => api.get<{ disks: StorageDisk[] }>("/api/server/storage/disks"),
  checkMove: async (target: string) => api.post<MoveCheck>("/api/server/storage/check", { target }),
  move: async (target: string) =>
    api.post<{ state: MoveState; target: string }>("/api/server/storage/move", { target }),
  /** Answers while the server is up (public; used to see it come back). */
  ping: async () => api.get<unknown>("/api/system/version"),
};
