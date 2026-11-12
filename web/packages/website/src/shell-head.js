// maktaba.tv's head script (story 60.01 part A, built in 60.03): loaded in the <head> before the
// page is drawn, so what it decides moves nothing (no layout shift). It marks that script runs
// (the phone menu folds the nav behind its button only then), and decides the language hint
// (Epic 60 README §6.5): on an English page in a browser whose first language is Arabic, the
// line that offers the Arabic page shows, unless it was hidden once on this browser
// (localStorage "maktaba.langHint", never a cookie). design/shell.js wires the Hide button.
(function () {
  var root = document.documentElement;
  root.classList.add("js");
  if (root.lang !== "en") return;
  try {
    if (localStorage.getItem("maktaba.langHint")) return;
  } catch {
    // storage refused (a private window): the hint shows, and hiding it lasts for the page
  }
  var langs =
    navigator.languages && navigator.languages.length
      ? navigator.languages
      : [navigator.language || ""];
  if (/^ar\b/i.test(langs[0] || "")) root.setAttribute("data-lang-hint", "");
})();
