// The home page's one script of its own (Epic 60 story 60.10): the Download button preselects
// the visitor's system on the download page. It reads the user agent in the browser and sends no
// request. It runs in the <head>, before the first paint: it names the system on <html>
// (data-platform), so the button shows "Download for Mac" or "Download for Windows" from its
// first frame (src/part-b.css) and nothing moves; once the page is read, each Download link to
// /download/ gets the system as its fragment (/download/#mac). A system it cannot tell, or no
// JavaScript: the button reads "Download Maktaba" and goes to /download/ with nothing chosen.
//
// The build publishes this file with its types and its `export`s stripped, under a
// content-hashed name, as a classic script (scripts/home.mjs). It imports nothing, so it runs as
// it is in the browser and in the tests (home.test.ts).

export type Platform = "windows" | "mac" | "linux" | "ios" | "android";

/** The visitor's system from its user agent (and, where the browser gives them, its client-hint
 * platform and touch points: iPadOS says "Macintosh" in its user agent), or null when the agent
 * does not say (ChromeOS, a bot, an agent that hides it). */
export function platformOf(ua: string, hint = "", touchPoints = 0): Platform | null {
  const h = hint.toLowerCase();
  if (h === "android" || /android/i.test(ua)) return "android";
  if (h === "ios" || /iphone|ipad|ipod/i.test(ua)) return "ios";
  if (h === "windows" || /windows/i.test(ua)) return "windows";
  if (h === "macos" || /macintosh|mac os x/i.test(ua)) return touchPoints > 1 ? "ios" : "mac";
  if (h === "chrome os" || /cros/i.test(ua)) return null;
  if (h === "linux" || /linux|x11/i.test(ua)) return "linux";
  return null;
}

/** A Download link with the system as its fragment: only a link to the download page, in either
 * language, and only when the system is known. */
export function preselect(href: string, platform: Platform | null): string {
  if (!platform || !/^(\/ar)?\/download\/(#.*)?$/.test(href)) return href;
  return `${href.replace(/#.*$/, "")}#${platform}`;
}

/** Names the system on <html>, then fragments the Download links once the page is read. */
export function start(doc: Document, nav: Navigator): void {
  const data = (nav as Navigator & { userAgentData?: { platform?: string } }).userAgentData;
  const platform = platformOf(nav.userAgent, data?.platform ?? "", nav.maxTouchPoints ?? 0);
  if (platform) doc.documentElement.dataset.platform = platform;
  const links = () => {
    for (const a of doc.querySelectorAll<HTMLAnchorElement>("a[data-download]"))
      a.setAttribute("href", preselect(a.getAttribute("href") ?? "", platform));
  };
  if (doc.readyState === "loading") doc.addEventListener("DOMContentLoaded", links);
  else links();
}

if (typeof document !== "undefined" && typeof navigator !== "undefined") start(document, navigator);
