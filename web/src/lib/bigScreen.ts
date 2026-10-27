// Epic 46 — entering and leaving big-screen mode (/big), from the normal UI,
// the `g b` shortcut and the desktop apps' View → Big Screen.
//
// Entering remembers the page the user was on (sessionStorage) and asks for
// full screen: the desktop shell's window (the set_big_screen command), or
// the browser's Fullscreen API (refused without a user gesture, which is
// harmless: big screen works in a window too). Leaving undoes both and goes
// back to the remembered page.
//
// The shell's commands (apps/desktop/src-tauri/src/bigscreen.rs):
//   big_screen_state            -> { start: bool, fullscreen: bool }
//   set_big_screen { on }       -> the window full screen or not
//   set_big_screen_start { on } -> "Start in big screen", saved on this machine
// A page the shell does not serve (the browser, or another machine's server
// in client mode) has none of them: every call here is best effort.
import { isDesktop } from "./desktop";

export const BIG_BASE = "/big";
const RETURN_KEY = "mkt:big:return";

export function isBigPath(pathname: string): boolean {
  return pathname === BIG_BASE || pathname.startsWith(`${BIG_BASE}/`);
}

// The command's answer (null for one that answers nothing), or undefined when
// there is no shell or it refused.
async function invoke<T>(cmd: string, args?: Record<string, unknown>): Promise<T | undefined> {
  if (!isDesktop()) return undefined;
  try {
    const core = await import("@tauri-apps/api/core");
    return (await core.invoke<T>(cmd, args)) ?? (null as T);
  } catch {
    return undefined;
  }
}

/** Full screen on or off: the desktop window, else the browser's. */
export async function setFullScreen(on: boolean): Promise<void> {
  // the shell's window; a page it does not serve falls back to the browser's
  if ((await invoke<null>("set_big_screen", { on })) !== undefined) return;
  try {
    if (on && !document.fullscreenElement) await document.documentElement.requestFullscreen?.();
    if (!on && document.fullscreenElement) await document.exitFullscreen?.();
  } catch {
    /* refused (no gesture) or unsupported: big screen works in a window */
  }
}

export interface DesktopBigScreen {
  start: boolean;
  fullscreen: boolean;
}

/** The desktop shell's big-screen state, or null outside the desktop apps. */
export async function desktopBigScreenState(): Promise<DesktopBigScreen | null> {
  const s = await invoke<DesktopBigScreen>("big_screen_state");
  return s && typeof s.start === "boolean" ? s : null;
}

/** Saves "Start in big screen" on this machine; false when the shell is not there. */
export async function setDesktopStartInBigScreen(on: boolean): Promise<boolean> {
  return (await invoke<null>("set_big_screen_start", { on })) !== undefined;
}

/** Remembers where the user was (not a big-screen page) to go back there on exit. */
export function rememberReturn(path: string) {
  if (isBigPath(path.split(/[?#]/)[0] ?? "")) return;
  try {
    sessionStorage.setItem(RETURN_KEY, path);
  } catch {
    /* storage blocked: exit goes Home */
  }
}

/** The page to go back to on exit (an in-app path, never a big-screen one). */
export function returnPath(): string {
  try {
    const p = sessionStorage.getItem(RETURN_KEY);
    if (p && p.startsWith("/") && !p.startsWith("//") && !isBigPath(p.split(/[?#]/)[0] ?? ""))
      return p;
  } catch {
    /* ignored */
  }
  return "/";
}

/** Enters big screen: remembers `from` and asks for full screen; the caller navigates to /big. */
export function enterBigScreen(from: string) {
  rememberReturn(from);
  void setFullScreen(true);
}
