// Entry of the TV build (story 58.08): index.tv.html → /tv/ on the home.
// The big screen's own theme (bigscreen.css) is always dark; the fonts are the
// web app's, bundled as WOFF2 (story 58.15): a TV's system fonts may lack Arabic.
import React from "react";
import ReactDOM from "react-dom/client";
import { installErrorLog } from "../lib/errorLog";
import { TvApp } from "./TvApp";
import "@fontsource/ibm-plex-sans-arabic/400.css";
import "@fontsource/ibm-plex-sans-arabic/600.css";
import "@fontsource/ibm-plex-sans-arabic/700.css";
import "@fontsource/amiri/400.css";
import "@fontsource/amiri/700.css";
import "../styles/tokens.css";
import "./tv.css";

installErrorLog();

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("tv: #root element missing from index.tv.html");

ReactDOM.createRoot(rootEl).render(
  <React.StrictMode>
    <TvApp />
  </React.StrictMode>
);
