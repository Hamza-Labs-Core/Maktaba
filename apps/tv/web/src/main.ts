// Entry of the Samsung and LG launcher (story 58.09): apps/tv/web/index.html, packaged as
// a .wgt for Tizen (58.10) and an .ipk for webOS (58.11). Its fonts are bundled (WOFF2):
// a TV's system fonts may lack Arabic (story 58.15).
import "@fontsource/ibm-plex-sans-arabic/400.css";
import "@fontsource/ibm-plex-sans-arabic/600.css";
import "@fontsource/amiri/700.css";
import "./styles.css";
import { Launcher } from "./app";
import { initLang } from "./i18n";
import { log } from "./log";
import { detectPlatform } from "./platform";

window.addEventListener("error", (e) => log("error", `uncaught: ${e.message}`));
window.addEventListener("unhandledrejection", (e) =>
  log("error", `unhandled: ${String(e.reason)}`)
);

initLang(navigator.language);
const launcher = new Launcher({
  window,
  platform: detectPlatform(window),
  fetch: (url, init) => fetch(url, { ...init, credentials: "omit", cache: "no-store" }),
});
void launcher.start();
