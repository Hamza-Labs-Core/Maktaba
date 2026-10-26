// Epic 45 (story 45.5): the library management API the Manage libraries page
// speaks. Every route is admin-only (the API answers 403 before any query).
//
//   GET    /api/libraries/manage                  { libraries: ManagedLibrary[], conflict_groups }
//   GET    /api/libraries/folders[?path=]         { places } | FolderListing (404: not a folder)
//   GET    /api/libraries/{id}/impact             Impact (counts)
//   POST   /api/libraries/{id}/merge              { into, confirm: <merged library's name> }
//   POST   /api/libraries/{id}/empty-trash        { removed }
//   POST   /api/libraries                         { name, roots, settings? }   (422: roots[i])
//   PATCH  /api/libraries/{id}                    { name?, roots?, settings? } (422: roots[i])
//   DELETE /api/libraries/{id}                    records only: the web never sends purge=true
import { api, ApiError } from "./api";

export interface ManagedRoot {
  /** the database's form of the folder */
  path: string;
  /** where this machine reaches it (the path map applied) */
  local_path?: string;
  reachable: boolean;
  /** why it is not reachable (not-found, permission, …) */
  problem?: string | null;
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
  created_at?: string;
  finished_at?: string | null;
  error?: string | null;
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
  settings?: Record<string, unknown> | null;
  created_at?: string;
  updated_at?: string;
  counts: LibraryCounts;
  last_scan: LastScan | null;
  scanning: boolean;
  conflicts?: RootConflict[];
}

/** A set of libraries sharing a folder; the target is the one with the most videos. */
export interface ConflictGroup {
  root?: string;
  kind?: "same" | "nested";
  library_ids: string[];
  target_id: string;
}

export interface ManageResponse {
  libraries: ManagedLibrary[];
  conflict_groups: ConflictGroup[];
}

export interface FolderEntry {
  name: string;
  path: string;
}

export interface FolderListing {
  path: string;
  local_path?: string;
  parent?: string | null;
  entries: FolderEntry[];
  truncated?: boolean;
}

export interface Impact {
  videos: number;
  transcripts: number;
  playback_states: number;
  watch_sessions: number;
  series: number;
  collections: number;
  channel_blocks: number;
}

export interface MergeResult {
  moved: number;
  duplicates_removed: number;
  roots_added: number;
}

// A group may name its libraries as ids or as {id, …} and its target as an id or
// an object; without a target the library with the most videos is suggested.
function normaliseGroup(raw: unknown, libraries: ManagedLibrary[]): ConflictGroup {
  const g = (raw ?? {}) as Partial<ConflictGroup> & {
    libraries?: Array<{ id?: string } | string>;
    target?: { id?: string } | string;
  };
  const ids =
    g.library_ids ??
    (g.libraries ?? [])
      .map((l) => (typeof l === "string" ? l : l?.id))
      .filter((id): id is string => typeof id === "string");
  const target =
    g.target_id ??
    (typeof g.target === "string" ? g.target : g.target?.id) ??
    [...ids].sort(
      (a, b) =>
        (libraries.find((l) => l.id === b)?.counts?.total ?? 0) -
        (libraries.find((l) => l.id === a)?.counts?.total ?? 0)
    )[0] ??
    "";
  return { root: g.root, kind: g.kind, library_ids: ids, target_id: target };
}

const lib = (id: string) => `/api/libraries/${encodeURIComponent(id)}`;

/** A place may come as a bare path or as {name, path}. */
function toEntry(p: unknown): FolderEntry | null {
  if (typeof p === "string") return p ? { name: p, path: p } : null;
  if (p && typeof p === "object" && typeof (p as FolderEntry).path === "string") {
    const e = p as Partial<FolderEntry> & { path: string };
    return { name: e.name || e.path, path: e.path };
  }
  return null;
}

export const libraryAdminApi = {
  async manage(): Promise<ManageResponse> {
    const r = await api.get<Partial<ManageResponse> | ManagedLibrary[] | null>(
      "/api/libraries/manage"
    );
    const libraries = (Array.isArray(r) ? r : r?.libraries) ?? [];
    const groups = (Array.isArray(r) ? [] : r?.conflict_groups) ?? [];
    return { libraries, conflict_groups: groups.map((g) => normaliseGroup(g, libraries)) };
  },
  async places(): Promise<FolderEntry[]> {
    const r = await api.get<{ places?: unknown[] } | null>("/api/libraries/folders");
    return (r?.places ?? []).map(toEntry).filter((e): e is FolderEntry => e !== null);
  },
  async folder(path: string): Promise<FolderListing> {
    const r = await api.get<FolderListing>(
      `/api/libraries/folders?path=${encodeURIComponent(path)}`
    );
    return { ...r, entries: r?.entries ?? [] };
  },
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
  return e instanceof ApiError ? e.problem.detail || e.problem.title : fallback;
}
