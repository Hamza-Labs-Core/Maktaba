// Theme boot (Story 11.8): stamps <html data-theme> BEFORE first paint so
// there is no flash of the wrong theme. Loaded as a classic blocking
// <script src> from index.html's <head>: the SPA's CSP (default-src 'self')
// forbids inline script, so this lives in its own same-origin file.
//
// Mirrors src/lib/theme.ts readMode()/resolveTheme(); keep the two in sync
// (src/lib/theme.test.ts runs this file against the same cases).
//
// Precedence: an explicit stored choice ("light" | "dark") > the OS
// preference ("system", the default) > dark. Only an OS that explicitly
// prefers light gets light; no preference, no matchMedia or any error
// resolves to dark.
(function () {
  var d = document.documentElement;
  try {
    var m = null;
    try {
      m = localStorage.getItem("mkt:theme");
    } catch (e) {
      m = null;
    }
    if (m !== "light" && m !== "dark" && m !== "system") m = "system";
    var light =
      m === "light" ||
      (m === "system" &&
        !!window.matchMedia &&
        window.matchMedia("(prefers-color-scheme: light)").matches);
    d.setAttribute("data-theme", light ? "light" : "dark");
  } catch (e) {
    d.setAttribute("data-theme", "dark");
  }
})();
