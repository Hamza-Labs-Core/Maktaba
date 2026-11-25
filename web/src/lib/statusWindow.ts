// The server's status in its own window of the desktop app (Epic 13 story 13.13; the design:
// docs/design/desktop/mockups/server-status.html). The shell (apps/desktop/src-tauri/src/
// ownwindows.rs, statusview.rs) opens it from the tray's Server status and serves its view:
//
//   status_window_state              -> StatusWindowView
//   status_window_action { action }  -> one of StatusAction (the tray's server actions, or a
//                                       page of this machine's web app in the main window)
//   event "status-window"            -> a new StatusWindowView, at each status refresh
//
// The page words everything itself (src/i18n), from the codes and values here.
import { isDesktop } from "./desktop";
import type { ServerStatus } from "./desktopSetup";

/** A part's state, as its dot and its value say it. */
export type PartTone = "error" | "busy" | "ok" | "idle" | "off";

export type PartId =
  | "database"
  | "server"
  | "playback"
  | "indexing"
  | "model"
  | "plugins"
  | "remote";

export interface StatusPart {
  id: PartId;
  tone: PartTone;
  /** how many of its processes run (with playback: the Live TV channels on air too) */
  running: number;
  /** what it waits on or why it fails (the server's English, its code and values) */
  detail?: string;
  detail_code?: string;
  detail_params?: Record<string, unknown>;
  /** a download or an install under way, 0 to 1 */
  progress?: number;
  /** the language model's name */
  model?: string;
  /** the model's bytes downloaded and to download */
  bytes?: [number, number];
}

export interface StatusUpdate {
  version: string;
  /** the time it installs by; "" for shortly */
  by: string;
  installing: boolean;
  /** a button installs it now (else it installs by itself) */
  install: boolean;
}

export interface StatusWindowView {
  status: ServerStatus;
  host: string;
  version: string;
  /** when the server started (RFC 3339) */
  since?: string;
  parts: StatusPart[];
  update?: StatusUpdate;
}

export type StatusAction =
  | "open"
  | "pause"
  | "resume"
  | "logs"
  | "report"
  | "settings"
  | "update"
  | "libraries"
  | "devices";

/** The tile each of the server's processes is in (statusview.rs PARTS): what it is called. */
export const PART_OF_ROLE: Record<string, string> = {
  postgres: "database",
  api: "server",
  streaming: "playback",
  "tv-slot": "playback",
  pipeline: "indexing",
  ollama: "model",
  plugins: "plugins",
  "plugin-runner": "plugins",
  relay: "remote",
};

export const STATUS_EVENT = "status-window";

interface PreviewWindow {
  /** the visual references (web/e2e/desktop): the view, without the shell */
  __MAKTABA_STATUS_PREVIEW__?: StatusWindowView;
}

function preview(): StatusWindowView | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as PreviewWindow).__MAKTABA_STATUS_PREVIEW__;
}

function isView(v: unknown): v is StatusWindowView {
  if (!v || typeof v !== "object") return false;
  const o = v as Record<string, unknown>;
  return !!o.status && typeof o.status === "object" && Array.isArray(o.parts);
}

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T | undefined> {
  if (!isDesktop()) return undefined;
  try {
    const core = await import("@tauri-apps/api/core");
    return await core.invoke<T>(cmd, args);
  } catch {
    return undefined;
  }
}

/** The view now; null outside the app's status window. */
export async function statusWindowState(): Promise<StatusWindowView | null> {
  const p = preview();
  if (p) return p;
  const v = await invoke<StatusWindowView>("status_window_state");
  return isView(v) ? v : null;
}

/** One of the window's buttons. */
export async function statusWindowAction(action: StatusAction): Promise<void> {
  if (preview()) return;
  await invoke("status_window_action", { action });
}

/** Calls `cb` with each new view; returns its stop. */
export async function onStatusWindow(cb: (v: StatusWindowView) => void): Promise<() => void> {
  if (preview() || !isDesktop()) return () => {};
  try {
    // this window's own events (ownwindows.rs sends them to it), not any window's
    const { getCurrentWebviewWindow } = await import("@tauri-apps/api/webviewWindow");
    return await getCurrentWebviewWindow().listen<StatusWindowView>(STATUS_EVENT, (e) => {
      if (isView(e.payload)) cb(e.payload);
    });
  } catch {
    return () => {};
  }
}

/** Whether the server can be resumed (else paused) from the window. */
export function canResume(status: ServerStatus): boolean {
  return status.kind === "paused" || status.kind === "stopped";
}

/** The head's tone: the status's colour. */
export function statusTone(status: ServerStatus): PartTone {
  switch (status.kind) {
    case "running":
      return "ok";
    case "starting":
      return "busy";
    case "problem":
    case "not_answering":
      return "error";
    default:
      return "idle";
  }
}
