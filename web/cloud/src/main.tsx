// Entry point for the Maktaba Cloud web app (web/cloud/).
import React from "react";
import ReactDOM from "react-dom/client";
import "@fontsource/ibm-plex-sans-arabic/400.css";
import "@fontsource/ibm-plex-sans-arabic/500.css";
import "@fontsource/ibm-plex-sans-arabic/600.css";
import "@fontsource/ibm-plex-sans-arabic/700.css";
import "@fontsource/amiri/400.css";
import "@fontsource/amiri/700.css";
import "../../src/styles/tokens.css";
import "./cloud.css";
import { applyResolvedTheme, readMode } from "../../src/lib/theme";
import { App } from "./App";

// the same light / dark choice as the home app (stored mode, else the OS)
applyResolvedTheme(readMode());

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("main: #root element missing from index.html");

ReactDOM.createRoot(rootEl).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
