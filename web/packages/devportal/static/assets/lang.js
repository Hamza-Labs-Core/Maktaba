// The EN/AR toggle. assets/i18n.js (written by the build) defines MAKTABA_STRINGS before
// this runs. The choice is kept per browser; the first visit follows the browser language.
(function () {
  var STRINGS = window.MAKTABA_STRINGS || { en: {}, ar: {} };
  var KEY = "maktaba.dev.lang";

  function stored() {
    try {
      return window.localStorage.getItem(KEY);
    } catch {
      return null;
    }
  }
  function remember(lang) {
    try {
      window.localStorage.setItem(KEY, lang);
    } catch {
      /* private mode: the toggle still works for this page */
    }
  }
  function initial() {
    var s = stored();
    if (s === "ar" || s === "en") return s;
    var nav = (navigator.languages && navigator.languages[0]) || navigator.language || "en";
    return /^ar\b/i.test(nav) ? "ar" : "en";
  }

  function apply(lang) {
    var root = document.documentElement;
    var dict = STRINGS[lang] || STRINGS.en;
    // only the portal's own chrome and pages switch direction; a source-language document
    // (the changelog, a plugin doc) keeps its own dir="ltr" block
    root.lang = lang;
    root.dir = lang === "ar" ? "rtl" : "ltr";
    var nodes = document.querySelectorAll("[data-i18n]");
    for (var i = 0; i < nodes.length; i++) {
      var k = nodes[i].getAttribute("data-i18n");
      if (dict[k] !== undefined) nodes[i].textContent = dict[k];
    }
    var labelled = document.querySelectorAll("[data-i18n-label]");
    for (var j = 0; j < labelled.length; j++) {
      var lk = labelled[j].getAttribute("data-i18n-label");
      if (dict[lk] !== undefined) labelled[j].setAttribute("aria-label", dict[lk]);
    }
    var t = document.querySelector("[data-i18n-title]");
    if (t && dict[t.getAttribute("data-i18n-title")]) {
      document.title = dict[t.getAttribute("data-i18n-title")] + " · maktaba.dev";
    }
  }

  var lang = initial();
  // direction before the first paint, strings once the page is parsed
  document.documentElement.dir = lang === "ar" ? "rtl" : "ltr";
  document.documentElement.lang = lang;

  document.addEventListener("DOMContentLoaded", function () {
    apply(lang);
    var btn = document.getElementById("lang-toggle");
    if (btn) {
      btn.addEventListener("click", function () {
        lang = lang === "ar" ? "en" : "ar";
        remember(lang);
        apply(lang);
      });
    }
  });
})();
