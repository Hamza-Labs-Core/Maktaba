// Maktaba's own dialog for the desktop shell's errors (Epic 13 story 13.11; the design:
// docs/design/desktop/mockups/dialog.html). The shell (apps/desktop/src-tauri/src/shelldialog.rs)
// opens it, hidden, in a window of the app's own pages marked before the page loads
// (window.__MAKTABA_PANEL__ = "dialog"):
//
//   shell_dialog_content             -> the newest error's words; the dialog shows
//   shell_dialog_choose { action }   -> "close" | "logs" (the dialog closes, then Open logs)
//   event "shell-dialog"             -> a newer error's words, while it is shown
import { isDesktop } from "./desktop";

export interface DialogContent {
  lang: "en" | "ar";
  brand: string;
  title: string;
  errors: string[];
  logs: boolean;
  close: string;
  seeLogs: string;
  /** counts the errors: a new one is a new dialog */
  revision: number;
}

interface DialogWindow {
  __MAKTABA_PANEL__?: string;
  /** the visual references and tests (web/e2e/desktop): the words, without the shell */
  __MAKTABA_DIALOG_PREVIEW__?: DialogContent;
}

function win(): DialogWindow {
  return (typeof window === "undefined" ? {} : window) as DialogWindow;
}

export function isShellDialog(): boolean {
  return win().__MAKTABA_PANEL__ === "dialog";
}

function isContent(v: unknown): v is DialogContent {
  const c = v as DialogContent;
  return (
    !!c &&
    (c.lang === "en" || c.lang === "ar") &&
    typeof c.title === "string" &&
    Array.isArray(c.errors) &&
    typeof c.close === "string" &&
    typeof c.seeLogs === "string" &&
    typeof c.brand === "string"
  );
}

/** The newest error's words, which also shows the dialog (null: nothing to show). */
export async function loadDialogContent(): Promise<DialogContent | null> {
  const p = win().__MAKTABA_DIALOG_PREVIEW__;
  if (p) return isContent(p) ? p : null;
  if (!isDesktop()) return null;
  try {
    const core = await import("@tauri-apps/api/core");
    const c = await core.invoke<DialogContent | null>("shell_dialog_content");
    return isContent(c) ? c : null;
  } catch {
    return null;
  }
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
