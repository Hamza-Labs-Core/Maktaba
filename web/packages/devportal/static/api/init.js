// The API reference: Redoc (vendored at build time, served from this site) over the
// OpenAPI description the page names (data-spec): /api/openapi.json, or on the Arabic page
// /ar/api/openapi.json, the same description with its prose in Arabic.
(function () {
  function start() {
    var el = document.getElementById("redoc");
    if (!el || !window.Redoc) return;
    window.Redoc.init(
      el.getAttribute("data-spec") || "/api/openapi.json",
      {
        hideDownloadButton: false,
        disableSearch: false,
        expandResponses: "200,201",
        theme: { colors: { primary: { main: "#8a6417" } } },
      },
      el
    );
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
