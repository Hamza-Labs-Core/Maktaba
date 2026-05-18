// Entry point for the Maktaba web app (Epic 11).
//
// The resolved theme is already on <html data-theme> from the inline
// boot script in index.html (Story 11.8, no FOUC); applyResolvedTheme
// here only reconciles after the bundle parses. The service worker is
// registered post-paint (Story 11.10).
import React from "react";
import ReactDOM from "react-dom/client";
import { applyResolvedTheme, readMode } from "./lib/theme";
import { registerServiceWorker } from "./lib/pwa";
import { App } from "./App";
import "./styles/tokens.css";
import "./styles/app.css";

applyResolvedTheme(readMode());

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("main: #root element missing from index.html");
}

ReactDOM.createRoot(rootEl).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);

registerServiceWorker();
