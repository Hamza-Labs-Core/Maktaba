// The download page's head script (story 60.11 T06): the visitor's platform first, with no
// request and nothing moved. It runs in the <head>, before the first paint: the address's
// #<platform> (the home page's Download links to /download/#mac), or else the user agent, names a
// platform whose card the page has (data-cards on this script), and <html data-platform> is set,
// which src/download.css draws as the wide "For this computer" card at the top. Once the page is
// parsed, that card is moved first in the document too, where it is already drawn, so the
// keyboard and a screen reader meet it first and nothing on screen moves. A phone, a tablet
// (an iPad says Macintosh, with a touch screen) or an unknown system gets no preselection.
//
// A classic script (no export: a module would run only after the page is parsed, and has no
// document.currentScript); src/download-platform.test.ts runs this very file against a stub page.

(function () {
  /** The platform a user agent names, or "" (a phone, a tablet, anything else). */
  function platformOf(ua: string, touchPoints = 0): string {
    if (/iPhone|iPad|iPod|Android|CrOS/i.test(ua)) return "";
    if (/Macintosh|Mac OS X/.test(ua)) return touchPoints > 1 ? "" : "mac";
    if (/Windows NT/.test(ua)) return "windows";
    if (/Linux|X11/.test(ua)) return "linux";
    return "";
  }

  /** The platform the page puts first: the address's, else the user agent's, if it has a card. */
  function chosen(cards: string[], hash: string, ua: string, touchPoints = 0): string {
    const asked = hash.replace(/^#/, "");
    if (cards.includes(asked)) return asked;
    const p = platformOf(ua, touchPoints);
    return cards.includes(p) ? p : "";
  }

  const me = document.currentScript;
  const cards = (me?.getAttribute("data-cards") ?? "").split(" ").filter(Boolean);
  const p = chosen(cards, location.hash, navigator.userAgent, navigator.maxTouchPoints || 0);
  if (!p) return;
  document.documentElement.setAttribute("data-platform", p);
  document.addEventListener("DOMContentLoaded", () => {
    const card = document.getElementById(p);
    const list = card?.parentElement;
    if (card && list && list.firstElementChild !== card)
      list.insertBefore(card, list.firstElementChild);
  });
})();
