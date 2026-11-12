// maktaba.tv, the website (Epic 60, story 60.03). Every request comes here first
// (run_worker_first): the Worker redirects www and http to https://maktaba.tv and the paths of
// src/redirects.json (/docs) to their pages, serves the static assets the build assembled
// (scripts/build.mjs), answers a missing path with the 404 page in its language, and puts the
// cache and security headers on every response (src/headers.ts). No backend, no KV, no secrets.

import { APEX, redirect, REDIRECTS, withHeaders } from "./headers";

export interface Env {
  ASSETS: { fetch(req: Request): Promise<Response> };
  // "off" in `npm run dev` and CI's `wrangler dev` smoke: wrangler dev hands the Worker the
  // route's own URL over plain http, which would redirect to https://maktaba.tv forever. On by
  // default (and deployed).
  CANONICAL_REDIRECTS?: string;
}

const isArabic = (path: string) => path === "/ar" || path.startsWith("/ar/");

/** The 404 page with the address asked for in its <code data-asked> (the design's 404,
 * story 60.01 part A): escaped, and cut at 200 characters. */
function withAsked(html: string, pathname: string): string {
  const shown = pathname.length > 200 ? `${pathname.slice(0, 200)}…` : pathname;
  const safe = shown
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
  return html.replace("<code data-asked></code>", () => `<code data-asked>${safe}</code>`);
}

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const url = new URL(req.url);
    const canonical = env.CANONICAL_REDIRECTS !== "off";

    // one canonical origin: https://maktaba.tv
    if (canonical && (url.hostname === `www.${APEX}` || url.protocol === "http:")) {
      return redirect(`https://${APEX}${url.pathname}${url.search}`);
    }
    if (req.method !== "GET" && req.method !== "HEAD") {
      const res = new Response("Method not allowed\n", {
        status: 405,
        headers: { Allow: "GET, HEAD", "Content-Type": "text/plain; charset=utf-8" },
      });
      return withHeaders(res, url.pathname);
    }
    // the site's redirects (src/redirects.json, which the link check reads too): /docs, the
    // address apps and old links print, to help in the visitor's language
    if (Object.hasOwn(REDIRECTS, url.pathname)) return redirect(REDIRECTS[url.pathname]);

    let res = await env.ASSETS.fetch(req);
    if (res.status === 404) {
      // the site's own 404 page, with its 404 status (the assets' 404-page handling does this
      // too; this keeps it true whatever the assets return), in Arabic under /ar/
      const notFound = isArabic(url.pathname) ? "/ar/404" : "/404";
      const page = await env.ASSETS.fetch(new Request(new URL(notFound, url), { method: "GET" }));
      const headers = new Headers(page.headers);
      if (!page.ok) headers.set("Content-Type", "text/plain; charset=utf-8");
      // the page names the address that was asked for (its <code data-asked>), escaped
      const body = page.ok ? withAsked(await page.text(), url.pathname) : "Not found\n";
      headers.delete("Content-Length");
      res = new Response(body, { status: 404, headers });
    }
    return withHeaders(res, url.pathname);
  },
};
