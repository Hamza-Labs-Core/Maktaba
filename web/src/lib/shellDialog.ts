// Maktaba's own dialog for the desktop shell's errors (Epic 13 story 13.11; the design:
// docs/design/desktop/mockups/dialog.html). The shell (apps/desktop/src-tauri/src/shelldialog.rs)
// opens it in a window of the app's own pages, marked before the page loads:
//
//   window.__MAKTABA_PANEL__ = "dialog"; window.__MAKTABA_DIALOG__ = { …DialogContent }
//   shell_dialog_choose { action }   -> "close" | "logs" (the tray's Open logs, then close)
//   event "shell-dialog"             -> new words, for a dialog already open
import { isDesktop } from "./desktop";

export interface DialogContent {
  lang: "en" | "ar";
  brand: string;
  title: string;
  errors: string[];
  logs: boolean;
  close: string;
  seeLogs: string;
}

interface DialogWindow {
  __MAKTABA_PANEL__?: string;
  __MAKTABA_DIALOG__?: DialogContent;
  /** the visual references (web/e2e/desktop): no shell to send the choice to */
  __MAKTABA_DIALOG_PREVIEW__?: boolean;
}

function win(): DialogWindow {
  return (typeof window === "undefined" ? {} : window) as DialogWindow;
}

export function isShellDialog(): boolean {
  return win().__MAKTABA_PANEL__ === "dialog";
}

function isContent(v: unknown): v is DialogContent {
  const c = v as DialogContent;
  return !!c && typeof c.title === "string" && Array.isArray(c.errors);
}

/** The words the shell gave the page before it loaded. */
export function dialogContent(): DialogContent | null {
  const c = win().__MAKTABA_DIALOG__;
  return isContent(c) ? c : null;
}

export async function chooseDialog(action: "close" | "logs"): Promise<void> {
  if (win().__MAKTABA_DIALOG_PREVIEW__ || !isDesktop()) return;
  try {
    const core = await import("@tauri-apps/api/core");
    await core.invoke("shell_dialog_choose", { action });
  } catch {
    /* the shell refused (no logs here): nothing to do */
  }
}

/** Calls `cb` with new words while the dialog is open; returns its stop. */
export function onDialogContent(cb: (c: DialogContent) => void): () => void {
  if (win().__MAKTABA_DIALOG_PREVIEW__ || !isDesktop()) return () => {};
  let stop = () => {};
  let live = true;
  void import("@tauri-apps/api/event")
    .then(({ listen }) =>
      listen<DialogContent>("shell-dialog", (e) => {
        if (isContent(e.payload)) cb(e.payload);
      })
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
