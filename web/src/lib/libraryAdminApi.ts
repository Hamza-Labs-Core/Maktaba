// Epic 45 (story 45.5): the library management API the Manage libraries page
// speaks. Every route is admin-only (the API answers 403 before any query).
//
//   GET    /api/libraries/manage                  { items: ManagedLibrary[], conflict_groups }
//   GET    /api/libraries/folders[?path=]         { places: Place[] } | FolderListing (404: not a folder)
//   GET    /api/libraries/{id}/impact             Impact (counts)
//   POST   /api/libraries/{id}/merge              { into, confirm: <merged library's name> } (412: name)
//   POST   /api/libraries/{id}/empty-trash        { removed }
//   POST   /api/libraries                         { name, roots, settings? }   (422: roots[i])
//   PATCH  /api/libraries/{id}                    { name?, roots?, settings? } (422: roots[i])
//   DELETE /api/libraries/{id}                    records only: the web never sends purge=true
import { api, ApiError } from "./api";

export interface ManagedRoot {
  /** the database's form of the folder */
  path: string;
  /** where this machine reaches it (the path map applied) */
  local_path: string;
  reachable: boolean;
  /** why it is not reachable (not-found, permission, …) */
  problem: string | null;
}

export interface LibraryCounts {
  total: number;
  missing: number;
  failed: number;
  ready: number;
  processing: number;
}

export interface LastScan {
  job_id: string;
  state: string;
  created_at: string;
  finished_at: string | null;
  error: string | null;
}

export interface RootConflict {
  root: string;
  other_root: string;
  library_id: string;
  library_name: string;
  kind: "same" | "nested";
  other_count: number;
}

export interface ManagedLibrary {
  id: string;
  name: string;
  roots: ManagedRoot[];
  settings: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  counts: LibraryCounts;
  last_scan: LastScan | null;
  scanning: boolean;
  conflicts: RootConflict[];
}

export interface ConflictMember {
  id: string;
  name: string;
  videos: number;
}

/** A set of libraries sharing a folder; the suggested target holds the most videos. */
export interface ConflictGroup {
  libraries: ConflictMember[];
  /** "same" when any two share a folder, else "nested" */
  kind: "same" | "nested";
  /** the id of the library to merge the others into */
  suggested_target: string;
}

export interface ManageResponse {
  items: ManagedLibrary[];
  conflict_groups: ConflictGroup[];
}

/** A starting folder of the picker (the path in the database's form). */
export interface Place {
  name: string;
  path: string;
  local_path: string;
}

export interface FolderEntry {
  name: string;
  path: string;
}

export interface FolderListing {
  path: string;
  local_path: string;
  /** null at the top of the disk */
  parent: string | null;
  entries: FolderEntry[];
  truncated: boolean;
}

export interface Impact {
  videos: number;
  transcripts: number;
  playback_states: number;
  watch_sessions: number;
  series: number;
  collections: number;
  channel_blocks: number;
  /** library access grants: the people who lose access (a merge never copies them) */
  grants: number;
}

export interface MergeResult {
  moved: number;
  duplicates_removed: number;
  roots_added: number;
  into: { id: string; name: string };
}

const lib = (id: string) => `/api/libraries/${encodeURIComponent(id)}`;

export const libraryAdminApi = {
  manage: async () => api.get<ManageResponse>("/api/libraries/manage"),
  async places(): Promise<Place[]> {
    return (await api.get<{ places: Place[] }>("/api/libraries/folders")).places;
  },
  folder: async (path: string) =>
    api.get<FolderListing>(`/api/libraries/folders?path=${encodeURIComponent(path)}`),
  impact: async (id: string) => api.get<Impact>(`${lib(id)}/impact`),
  merge: async (id: string, into: string, confirm: string) =>
    api.post<MergeResult>(`${lib(id)}/merge`, { into, confirm }),
  emptyTrash: async (id: string) => api.post<{ removed: number }>(`${lib(id)}/empty-trash`),
  create: async (body: { name: string; roots: string[]; settings?: Record<string, unknown> }) =>
    api.post<{ id: string; warnings?: string[] }>("/api/libraries", body),
  update: async (
    id: string,
    body: { name?: string; roots?: string[]; settings?: Record<string, unknown> }
  ) => api.patch<{ id: string; warnings?: string[] }>(lib(id), body),
  // never ?purge=true: removing a library from the web keeps its files on disk
  remove: async (id: string) => api.delete<void>(lib(id)),
};

export interface FieldError {
  field: string;
  message: string;
}

/** The field errors of a 422 problem (`errors: [{field, message}]`), else []. */
export function fieldErrors(e: unknown): FieldError[] {
  if (!(e instanceof ApiError)) return [];
  const errs = (e.problem as { errors?: unknown }).errors;
  if (!Array.isArray(errs)) return [];
  return errs.filter(
    (x): x is FieldError =>
      !!x && typeof x === "object" && typeof (x as FieldError).field === "string"
  );
}

/** The message to show for a failed call. */
export function problemMessage(e: unknown, fallback: string): string {
  if (!(e instanceof ApiError)) return fallback;
  // a supervisor's answer passed through ({error} or {errors}) has no title
  const raw = (e.problem as { error?: unknown }).error;
  return e.problem.detail || e.problem.title || (typeof raw === "string" && raw) || fallback;
}
