// The API reference: Redoc (vendored at build time, served from this site) over the
// OpenAPI description the build copied from shared/api/openapi.json.
(function () {
  function start() {
    var el = document.getElementById("redoc");
    if (!el || !window.Redoc) return;
    window.Redoc.init(
      "/api/openapi.json",
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
