// The Mac app's first-run setup bridge (Epic 36, story 36.6).
//
// The setup screens (pages/Setup/) run inside Maktaba.app's bundled web
// app and talk to the Rust shell (apps/desktop/src-tauri/src/shell.rs):
// each screen runs one `maktaba-server setup --json` step
// (specs/epics/36-mac-app/contract-setup.md). The screens take a
// SetupApi so tests can drive them with a fake.
import { isDesktop } from "./desktop";

/** What the main window should show, from the shell's saved mode. */
export type AppTarget =
  | { target: "setup" }
  | { target: "starting"; url: string }
  | { target: "remote"; url: string }
  | { target: "bundled" };

export type LoginItem =
  | "not_registered"
  | "enabled"
  | "requires_approval"
  | "not_found"
  | "unsupported";

/** The OS the app runs on (Rust's std::env::consts::OS). */
export type Platform = "macos" | "windows" | "linux";

export type AppState = AppTarget & {
  server_available: boolean;
  login_item: LoginItem;
  /** the OS the app runs on (Epic 38); absent from an Epic 36 shell: a Mac */
  platform?: Platform;
  /** the user asked to change servers: do not adopt this Mac's configured server */
  reset?: boolean;
};

/** The server's status; `detail` is what it waits on while it starts (the
 * venv being built, the backup before an update) or why that failed. */
export type ServerStatus = (
  | { kind: "setup_needed" | "paused" | "stopped" | "starting" | "running" }
  | { kind: "problem"; roles: string[] }
) & { detail?: string };

export interface StepProgress {
  stage: string;
  message: string;
  done?: number;
  total?: number;
}

/** A failed step (setup.rs StepError). */
export interface StepError {
  code: string;
  /** English, for a code the app has no text for (and the details box) */
  message: string;
  field?: string;
  /** the values the code's text takes (setup.error.<code>) */
  params?: Record<string, unknown>;
  details: string[];
}

/** One finding of the check step: setup.check.note.<code> with its params. */
export interface CheckNote {
  code: string;
  params?: Record<string, unknown>;
  message: string;
}

export function isStepError(e: unknown): e is StepError {
  return typeof e === "object" && e !== null && "code" in e && "message" in e;
}

export type StepName =
  | "status"
  | "check"
  | "admin"
  | "libraries"
  | "models"
  | "plex"
  | "venv"
  | "llm"
  | "finish";

export interface SetupApi {
  appState(): Promise<AppState>;
  /** Rejects with a StepError. */
  runStep<T = Record<string, unknown>>(
    step: StepName,
    input: Record<string, unknown>,
    onProgress?: (p: StepProgress) => void
  ): Promise<T>;
  finishSetup(webUrl: string, supervisorUrl?: string): Promise<LoginItem>;
  openLoginItems(): Promise<void>;
  serverStatus(): Promise<ServerStatus>;
  openLocalServer(): Promise<void>;
  useRemoteServer(url: string): Promise<void>;
  openExternal(url: string): Promise<void>;
}

/**
 * True only on the app's own bundled pages (tauri://localhost or
 * http://tauri.localhost), where the setup commands are allowed. The
 * local server's web app also runs inside the shell but must never
 * show setup (under `tauri dev` the pages come from Vite's :5173).
 */
export function isBundledDesktop(dev: boolean = import.meta.env.DEV): boolean {
  if (!isDesktop()) return false;
  const { protocol, hostname, port } = window.location;
  if (protocol === "tauri:" || hostname === "tauri.localhost") return true;
  // `tauri dev` serves these pages from the Vite dev server (devUrl)
  return dev && hostname === "localhost" && port === "5173";
}

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const core = await import("@tauri-apps/api/core");
  return core.invoke<T>(cmd, args);
}

export const tauriSetupApi: SetupApi = {
  appState: () => invoke<AppState>("app_state"),
  async runStep<T>(
    step: StepName,
    input: Record<string, unknown>,
    onProgress?: (p: StepProgress) => void
  ) {
    const { Channel } = await import("@tauri-apps/api/core");
    const ch = new Channel<StepProgress>();
    if (onProgress) ch.onmessage = onProgress;
    return invoke<T>("setup_step", { step, input, onProgress: ch });
  },
  finishSetup: (webUrl, supervisorUrl) =>
    invoke<LoginItem>("finish_setup", { webUrl, supervisorUrl }),
  openLoginItems: () => invoke<void>("open_login_items"),
  serverStatus: () => invoke<ServerStatus>("server_status"),
  openLocalServer: () => invoke<void>("open_local_server"),
  useRemoteServer: (url) => invoke<void>("use_remote_server", { url }),
  openExternal: (url) => invoke<void>("open_external", { url }),
};

/** "3.2 GB" — sizes for the disk/RAM check and download progress. */
export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "?";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${i === 0 ? n : n.toFixed(1)} ${units[i]}`;
}

/** 0–100 for a progress line, or null when it has no known total. */
export function progressPercent(p: StepProgress | null): number | null {
  if (!p || !p.total || p.done === undefined) return null;
  return Math.min(100, Math.max(0, (p.done / p.total) * 100));
}
