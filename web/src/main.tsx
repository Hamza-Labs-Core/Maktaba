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
import { isTrayPanel } from "./lib/trayPanel";
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
// story 13.12: the tray's panel
import "./styles/trayPanel.css";
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

// story 13.12: the desktop app's tray panel is a window of these pages that draws the panel
// instead of the app (traypanel.rs marks it before the page loads)
const TrayPanel = React.lazy(() =>
  import("./tray/TrayPanel").then((m) => ({ default: m.TrayPanel }))
);

ReactDOM.createRoot(rootEl).render(
  <React.StrictMode>
    {isTrayPanel() ? (
      <React.Suspense fallback={null}>
        <TrayPanel />
      </React.Suspense>
    ) : (
      <App />
    )}
  </React.StrictMode>
);

if (!isTrayPanel()) {
  registerServiceWorker();
  // a page an older release left open: its chunks are gone after an update
  reloadOnStaleChunks();
}
