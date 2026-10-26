// The Mac server app's permissions walkthrough: what the server needs from
// macOS (Full Disk Access, Local Network, its login item, keeping the Mac
// awake), whether each is on, and the System Settings pane that turns it on.
// The shell answers (apps/desktop/src-tauri/src/permissions.rs): the setup's
// Permissions step asks it on the bundled pages, the reminder in this Mac's
// own web app (http://127.0.0.1:<port>) asks it too. Everywhere else (a
// browser, the app connected to another Maktaba, an older shell) nothing is
// shown.
import { isDesktop } from "./desktop";
import { isBundledDesktop } from "./desktopSetup";

export type Grant = "granted" | "denied" | "not_asked" | "unknown";

/** The panes the shell may open: names, never URLs. */
export type Pane = "full_disk_access" | "local_network" | "login_items" | "energy";

export type PermissionId = "full_disk_access" | "local_network" | "login_item" | "keep_awake";

export interface PermissionItem {
  id: PermissionId;
  required: boolean;
  state: Grant;
  pane: Pane;
}

export interface PermissionsReport {
  platform: string;
  /** this Mac runs the server */
  server_mode: boolean;
  /** the walkthrough was shown once */
  seen: boolean;
  /** empty off a Mac */
  items: PermissionItem[];
}

export interface PermissionsApi {
  /** Rejects when the shell has no such command (an older app) or forbids it. */
  report(): Promise<PermissionsReport>;
  openPane(pane: Pane): Promise<void>;
  markSeen(): Promise<void>;
}

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const core = await import("@tauri-apps/api/core");
  return core.invoke<T>(cmd, args);
}

export const tauriPermissionsApi: PermissionsApi = {
  report: () => invoke<PermissionsReport>("host_permissions"),
  openPane: (pane) => invoke<void>("open_privacy_pane", { pane }),
  markSeen: () => invoke<void>("permissions_seen"),
};

/** This Mac's own server's web app inside the shell (the one origin, besides the
 * bundled pages, the shell lets ask). */
export function isLocalServerPage(): boolean {
  if (!isDesktop() || isBundledDesktop()) return false;
  const h = window.location.hostname;
  return h === "127.0.0.1" || h === "localhost" || h === "[::1]";
}

/** A required item that is off or not asked yet; unknown is not counted (Local
 * Network: macOS tells no app). */
export function isMissing(it: PermissionItem): boolean {
  return it.required && (it.state === "denied" || it.state === "not_asked");
}

export function missingRequired(r: PermissionsReport | null): PermissionItem[] {
  return r ? r.items.filter(isMissing) : [];
}
