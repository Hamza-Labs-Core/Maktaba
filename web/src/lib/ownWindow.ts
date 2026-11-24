// The desktop app's own windows of these pages (Epic 13 story 13.13): the shell marks a window
// before its page loads (window.__MAKTABA_PANEL__, apps/desktop/src-tauri/src/ownwindows.rs),
// so the page knows which it is:
//
//   "setup"   the first-run setup in its own 960 × 640 window (the app, which shows the setup)
//   "status"  the server's status in its own window (main.tsx draws StatusWindow)
//
// Any other page (the main window, the browser) is none of them.

export type OwnWindow = "setup" | "status";

interface MarkedWindow {
  __MAKTABA_PANEL__?: unknown;
}

/** Which of the app's own windows this page is in, or null. */
export function ownWindow(): OwnWindow | null {
  if (typeof window === "undefined") return null;
  const mark = (window as unknown as MarkedWindow).__MAKTABA_PANEL__;
  return mark === "setup" || mark === "status" ? mark : null;
}
