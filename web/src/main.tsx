// Entry point for the Maktaba web app (Epic 11).
//
// Mounts the React tree under #root. Theme initialisation runs before
// the first paint so the dark/light pick from localStorage doesn't
// flicker (Story 11.8 AC-3).
import React from "react";
import ReactDOM from "react-dom/client";
import { applyInitialTheme } from "./lib/theme";
import { bootNative } from "./lib/native";
import { App } from "./App";
import "./styles/tokens.css";
import "./styles/app.css";

applyInitialTheme();

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
    <App />
  </React.StrictMode>
);
