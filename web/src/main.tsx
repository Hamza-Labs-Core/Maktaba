// Entry point for the Maktaba web app (Epic 11).
//
// The resolved theme is already on <html data-theme> from the inline
// boot script in index.html (Story 11.8, no FOUC); applyResolvedTheme
// here only reconciles after the bundle parses. The service worker is
// registered post-paint (Story 11.10).
import React from "react";
import ReactDOM from "react-dom/client";
import { applyResolvedTheme, readMode } from "./lib/theme";
import { bootNative } from "./lib/native";
import { installErrorLog } from "./lib/errorLog";
import { installCrashReports } from "./lib/crashReports";
import { registerServiceWorker, reloadOnStaleChunks } from "./lib/pwa";
import { App } from "./App";
import { ownWindow } from "./lib/ownWindow";
import { I18nProvider } from "./lib/i18n";
import { DesktopFrame } from "./components/DesktopFrame";
import { StatusWindow } from "./status/StatusWindow";
import { isTrayPanel } from "./lib/trayPanel";
import { TrayPanel } from "./tray/TrayPanel";
import { isShellDialog } from "./lib/shellDialog";
import { ShellDialog } from "./dialog/ShellDialog";
// the theme's fonts, bundled (SIL OFL): Plex for text, Amiri for headings
import "@fontsource/ibm-plex-sans-arabic/400.css";
import "@fontsource/ibm-plex-sans-arabic/500.css";
import "@fontsource/ibm-plex-sans-arabic/600.css";
import "@fontsource/ibm-plex-sans-arabic/700.css";
import "@fontsource/amiri/400.css";
import "@fontsource/amiri/700.css";
import "./styles/tokens.css";
import "./styles/app.css";
// story 13.10: the desktop app's own window frame
import "./styles/frame.css";
// story 13.13: the desktop app's setup and status windows
import "./styles/desktopWindows.css";
// story 13.12: the tray's panel
import "./styles/trayPanel.css";
// story 13.11: the shell's own dialog
import "./styles/shellDialog.css";
import "./styles/library.css";
import "./styles/browse.css";
import "./styles/connect.css";

// first, so a throw during boot is logged too
installErrorLog();
// story 53.14: uncaught errors go to the home only when this person turned crash reports on
installCrashReports();

applyResolvedTheme(readMode());

// Wire the native lifecycle bridge (Story 12.1 / 12.2). Fire-and-forget:
// the web-side consumers install synchronously inside bootNative; the
// Capacitor bridge (native builds only) loads asynchronously after.
void bootNative();

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("main: #root element missing from index.html");
}

ReactDOM.createRoot(rootEl).render(
  <React.StrictMode>
    {/* the desktop app's own windows of these pages, marked before the page loads: the tray's
        panel (story 13.12, traypanel.rs), the shell's dialog for its errors (13.11,
        shelldialog.rs) and the server's status (13.13, ownwindows.rs) draw that instead of the
        app; the setup window is the app, which shows the setup */}
    {isTrayPanel() ? (
      <TrayPanel />
    ) : isShellDialog() ? (
      <ShellDialog />
    ) : ownWindow() === "status" ? (
      <I18nProvider>
        <DesktopFrame />
        <StatusWindow />
      </I18nProvider>
    ) : (
      <App />
    )}
  </React.StrictMode>
);

if (ownWindow() === null && !isTrayPanel() && !isShellDialog()) {
  registerServiceWorker();
  // a page an older release left open: its chunks are gone after an update
  reloadOnStaleChunks();
}
