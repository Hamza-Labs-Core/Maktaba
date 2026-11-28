// The tray's panel in the desktop app (Epic 13 story 13.12; the design:
// docs/design/desktop/mockups/tray.html). The shell (apps/desktop/src-tauri/src/traypanel.rs)
// opens it on a left click on the tray icon, in a window of the app's own pages, and marks it
// with window.__MAKTABA_PANEL__ = "tray" before the page loads (main.tsx draws the panel then):
//
//   tray_panel_state           -> TrayPanelState: the tray menu's entries, as they are now
//   tray_panel_choose { id }   -> choose one (an enabled item); the panel hides
//   tray_panel_hide            -> Escape
//   event "tray-panel-state"   -> the entries changed (nothing in it: the panel asks for them)
//   event "tray-panel-opened"  -> the shell showed the panel again (its first action takes the focus)
//
// The labels are the tray's own (lang.rs), in the system's language.
import { isDesktop } from "./desktop";

export interface TrayPanelEntry {
  id: string;
  label: string;
  enabled: boolean;
  separator: boolean;
}

export type TrayTone = "ok" | "busy" | "idle" | "error" | "none";

export interface TrayPanelState {
  lang: "en" | "ar";
  tone: TrayTone;
  entries: TrayPanelEntry[];
}

interface PanelWindow {
  __MAKTABA_PANEL__?: string;
  /** the visual references (web/e2e/desktop): the state, without the shell */
  __MAKTABA_TRAY_PREVIEW__?: TrayPanelState;
}

function win(): PanelWindow {
  return (typeof window === "undefined" ? {} : window) as PanelWindow;
}

/** This page is the tray's panel, not the app. */
export function isTrayPanel(): boolean {
  return win().__MAKTABA_PANEL__ === "tray";
}

async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T | undefined> {
  if (!isDesktop()) return undefined;
  try {
    const core = await import("@tauri-apps/api/core");
    return (await core.invoke<T>(cmd, args)) ?? (null as T);
  } catch {
    return undefined;
  }
}

function isState(v: unknown): v is TrayPanelState {
  return !!v && typeof v === "object" && Array.isArray((v as TrayPanelState).entries);
}

export async function trayPanelState(): Promise<TrayPanelState | null> {
  const p = win().__MAKTABA_TRAY_PREVIEW__;
  if (p) return p;
  const s = await invoke<TrayPanelState>("tray_panel_state");
  return isState(s) ? s : null;
}

export async function chooseTrayItem(id: string): Promise<void> {
  if (win().__MAKTABA_TRAY_PREVIEW__) return;
  await invoke("tray_panel_choose", { id });
}

export async function hideTrayPanel(): Promise<void> {
  if (win().__MAKTABA_TRAY_PREVIEW__) return;
  await invoke("tray_panel_hide");
}

/**
 * Calls `cb` with the entries each time they change; returns its stop. Its own window's events,
 * which say only that they changed: the entries come from the shell, so no page can give others.
 */
export async function onTrayPanelState(cb: (s: TrayPanelState) => void): Promise<() => void> {
  if (win().__MAKTABA_TRAY_PREVIEW__ || !isDesktop()) return () => {};
  try {
    const { getCurrentWebviewWindow } = await import("@tauri-apps/api/webviewWindow");
    let asked = 0;
    return await getCurrentWebviewWindow().listen("tray-panel-state", () => {
      const n = ++asked;
      void trayPanelState().then((s) => {
        // an answer to an earlier change that came late is not the newest
        if (s && n === asked) cb(s);
      });
    });
  } catch {
    return () => {};
  }
}

/** Calls `cb` each time the shell shows the panel again; returns its stop. */
export function onTrayPanelOpened(cb: () => void): () => void {
  if (win().__MAKTABA_TRAY_PREVIEW__ || !isDesktop()) return () => {};
  let stop = () => {};
  let live = true;
  void import("@tauri-apps/api/webviewWindow")
    .then(({ getCurrentWebviewWindow }) =>
      getCurrentWebviewWindow().listen("tray-panel-opened", () => cb())
    )
    .then((f) => {
      if (live) stop = f;
      else f();
    })
    .catch(() => {});
  return () => {
    live = false;
    stop();
  };
}

/** The two ways to quit, drawn as the panel's buttons rather than its rows. */
export const QUIT_IDS: ReadonlySet<string> = new Set(["quit", "stop-and-quit"]);
/** Lines of text, not actions. */
export const LINE_IDS: ReadonlySet<string> = new Set(["status", "model-status"]);
