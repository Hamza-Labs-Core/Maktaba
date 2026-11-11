// Epic 59 story 59.03: what Maktaba itself may do in a library's folders (its
// own user's permissions on the disk, not who may open the library).
//
//   GET  /api/libraries/{id}/access        LibraryAccessReport (admin)
//   POST /api/libraries/{id}/check-access  LibraryAccessReport, checked now (admin)
//
// A library whose access is "ro" or "none" is read-only for Maktaba: the API
// answers 409 `library_read_only` to every action that writes in its folders
// (convert, dedup, YouTube downloads, repair, empty trash, delete, restore).
// A null access was never checked and is treated as read and write.
import { useEffect, useState } from "react";
import { api, ApiError } from "./api";
import { libraryAdminApi } from "./libraryAdminApi";

export type LibraryAccess = "rw" | "ro" | "none";

/** The fix for a root, in the platform's terms (library.fix.<dsm|truenas|unraid|other>). */
export interface AccessFix {
  key: string;
  params: Record<string, string>;
}

export interface RootAccess {
  path: string;
  access: LibraryAccess | null;
  partial: boolean;
  /** up to 5 example files it could not read */
  unreadable: string[];
  /** the folder is mounted read-only in the container */
  mount_read_only: boolean;
  /** null when it is read and write with every sampled file readable */
  fix: AccessFix | null;
}

export interface RunsAs {
  uid: number;
  gid: number;
  name: string;
}

export interface LibraryAccessReport {
  library_id: string;
  access: LibraryAccess | null;
  partial: boolean;
  checked_at: string | null;
  roots: RootAccess[];
  /** newest first, up to 5 */
  write_failures: { path: string; at: string }[];
  runs_as: RunsAs | null;
  platform: string;
}

const lib = (id: string) => `/api/libraries/${encodeURIComponent(id)}`;

export const libraryAccessApi = {
  get: async (id: string) => api.get<LibraryAccessReport>(`${lib(id)}/access`),
  check: async (id: string) => api.post<LibraryAccessReport>(`${lib(id)}/check-access`),
};

/** Whether Maktaba may not write in a library with this access (never checked: it may). */
export function isReadOnly(access: LibraryAccess | null | undefined): boolean {
  return access === "ro" || access === "none";
}

/** The badge's key: library.access.<rw|ro|none|partial> (partial only when it can write). */
export function accessKey(access: LibraryAccess | null | undefined, partial: boolean): string {
  if (access === "ro" || access === "none") return `library.access.${access}`;
  return partial ? "library.access.partial" : "library.access.rw";
}

export function accessTone(
  access: LibraryAccess | null | undefined,
  partial: boolean
): "success" | "warn" | "error" {
  if (access === "none") return "error";
  if (access === "ro" || partial) return "warn";
  return "success";
}

type T = (key: string, vars?: Record<string, string | number>) => string;

/** The fix sentence for a root; "" when there is none or the key is unknown. */
export function fixText(fix: AccessFix | null | undefined, t: T): string {
  if (!fix || typeof fix.key !== "string" || !fix.key.startsWith("library.fix.")) return "";
  const s = t(fix.key, fix.params ?? {});
  return s === fix.key ? "" : s;
}

/**
 * Why an action a library's access rules out is not offered (story 59.16). An admin reads
 * the reason with the fix (`fix`, the folder's, may name a share or a uid); anyone else only
 * `library.not_available_here`, which says nothing of the home's folders or users.
 */
export function libraryRefusalText(admin: boolean, t: T, fix = ""): string {
  if (!admin) return t("library.not_available_here");
  return fix ? `${t("library.read_only")}. ${fix}` : t("library.read_only");
}

/**
 * A 409 `library_read_only` or `library_no_access` in the app's words; null otherwise. A
 * caller who is not an admin (`admin` false) reads `library.not_available_here`.
 */
export function libraryAccessErrorText(e: unknown, t: T, admin = true): string | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null;
  const code = e.problem.code;
  if (code !== "library_read_only" && code !== "library_no_access") return null;
  return admin ? t(`error.${code}`) : t("library.not_available_here");
}

/**
 * The ids of the libraries that are read-only for Maktaba (admin: it reads the
 * manage list). Empty until it answers, and on an older API that has no access.
 */
export function useReadOnlyLibraries(enabled = true): Set<string> {
  const [ids, setIds] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    libraryAdminApi
      .manage()
      .then((m) => {
        if (!live) return;
        const items = Array.isArray(m?.items) ? m.items : [];
        setIds(new Set(items.filter((l) => isReadOnly(l.access)).map((l) => l.id)));
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [enabled]);
  return ids;
}
