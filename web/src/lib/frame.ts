// Maktaba's own window frame in the desktop app (Epic 13 story 13.10; the design:
// docs/design/desktop/README.md). The shell (apps/desktop/src-tauri/src/frame.rs) gives the
// window to a page that claims it; this module is the page's side:
//
//   frame_claim                -> FrameInfo: which controls to draw, and where
//   frame_action { action }    -> "minimize" | "maximize" (toggles) | "close" | "snap"
//   frame_menu_items           -> the menu bar's items, for the title bar's menu
//   frame_menu { id }          -> choose one
//   event "frame-state"        -> { maximized, fullscreen? } when the window changes
//
// A page the shell cannot answer (the browser; another home's pages in client mode) gets
// null from claimFrame() and draws nothing: the window keeps the system's frame. Every call
// is best effort.
import { isDesktop } from "./desktop";

export type FrameControls = "windows" | "gtk" | "mac";

export interface FrameInfo {
  controls: FrameControls;
  /** Linux: gtk-decoration-layout ("appmenu:close", ":minimize,maximize,close"); else "" */
  layout: string;
  /** "end" (Windows, GNOME: at the end of the page's direction); Mac: "left" or "right" */
  side: "end" | "left" | "right";
  maximized: boolean;
  /** full screen (big screen): the page draws no frame */
  fullscreen?: boolean;
  snapLayouts: boolean;
}

export interface FrameMenuEntry {
  id: string;
  label: string;
  enabled: boolean;
  checked?: boolean;
  separator: boolean;
}

export interface FrameMenuGroup {
  title: string;
  items: FrameMenuEntry[];
}

export type FrameAction = "minimize" | "maximize" | "close" | "snap";

/** How long the pointer rests on the maximize button before Snap Layouts open (Windows' own). */
export const SNAP_DELAY_MS = 600;

/**
 * A preview of the frame without the shell, for the visual references
 * (web/e2e/desktop): `window.__MAKTABA_FRAME_PREVIEW__ = { controls: "windows", ... }`
 * before the app loads.
 */
interface PreviewWindow {
  __MAKTABA_FRAME_PREVIEW__?: Partial<FrameInfo> & { menu?: FrameMenuGroup[] };
}

function preview(): (Partial<FrameInfo> & { menu?: FrameMenuGroup[] }) | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as unknown as PreviewWindow).__MAKTABA_FRAME_PREVIEW__;
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

function isInfo(v: unknown): v is FrameInfo {
  if (!v || typeof v !== "object") return false;
  const o = v as Record<string, unknown>;
  return (
    (o.controls === "windows" || o.controls === "gtk" || o.controls === "mac") &&
    typeof o.maximized === "boolean"
  );
}

/** Takes the window's frame from the shell; null when this page cannot (keep the system's). */
export async function claimFrame(): Promise<FrameInfo | null> {
  const p = preview();
  if (p) {
    return {
      controls: p.controls ?? "windows",
      layout: p.layout ?? "",
      side: p.side ?? "end",
      maximized: p.maximized ?? false,
      fullscreen: p.fullscreen ?? false,
      snapLayouts: p.snapLayouts ?? false,
    };
  }
  const info = await invoke<FrameInfo>("frame_claim");
  return isInfo(info) ? info : null;
}

export async function frameAction(action: FrameAction): Promise<void> {
  if (preview()) return;
  await invoke("frame_action", { action });
}

export async function frameMenuItems(): Promise<FrameMenuGroup[]> {
  const p = preview();
  if (p) return p.menu ?? [];
  const groups = await invoke<FrameMenuGroup[]>("frame_menu_items");
  return Array.isArray(groups) ? groups : [];
}

export async function chooseFrameMenu(id: string): Promise<void> {
  if (preview()) return;
  await invoke("frame_menu", { id });
}

export interface FrameState {
  maximized: boolean;
  fullscreen?: boolean;
}

/** Calls `cb` when the window is maximized, restored or made full screen; returns its stop. */
export async function onFrameState(cb: (s: FrameState) => void): Promise<() => void> {
  if (preview() || !isDesktop()) return () => {};
  try {
    const { listen } = await import("@tauri-apps/api/event");
    return await listen<FrameState>("frame-state", (e) => {
      if (e.payload && typeof e.payload.maximized === "boolean") cb(e.payload);
    });
  } catch {
    return () => {};
  }
}

export type GtkButton = "minimize" | "maximize" | "close";

/**
 * GTK's button layout, as the buttons before and after the title: "appmenu:close" →
 * { start: [], end: ["close"] }. GNOME's default when the setting is empty or unreadable.
 * "start" and "end" follow the page's direction, as GTK mirrors the header bar in an RTL
 * locale.
 */
export function gtkButtons(layout: string): { start: GtkButton[]; end: GtkButton[] } {
  const known = (s: string): GtkButton[] =>
    s
      .split(",")
      .map((b) => b.trim())
      .filter((b): b is GtkButton => b === "minimize" || b === "maximize" || b === "close");
  const [before = "", after = ""] = (layout || "appmenu:close").split(":");
  const start = known(before);
  const end = known(after);
  if (start.length === 0 && end.length === 0) return { start: [], end: ["close"] };
  return { start, end };
}
