// maktaba.dev, the developer portal. Every request comes here first (run_worker_first): the
// Worker redirects www and http to https://maktaba.dev, serves the static assets the build
// assembled (scripts/build.mjs) and puts the cache and security headers on every response.
// No backend, no KV, no secrets.

import { APEX, redirect, withHeaders } from "./headers";

export interface Env {
  ASSETS: { fetch(req: Request): Promise<Response> };
  // "off" in `npm run dev`: wrangler dev hands the Worker the route's own URL over plain
  // http, which would redirect to https://maktaba.dev forever. On by default (and deployed).
  CANONICAL_REDIRECTS?: string;
}

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const url = new URL(req.url);
    const canonical = env.CANONICAL_REDIRECTS !== "off";

    // one canonical origin: https://maktaba.dev
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
    // RFC 9116 §3: the file lives under /.well-known/; the old root path points there
    if (url.pathname === "/security.txt") return redirect("/.well-known/security.txt");

    let res = await env.ASSETS.fetch(req);
    if (res.status === 404) {
      // the portal's own 404 page, with its 404 status (the assets' 404-page handling
      // does this too; this keeps it true whatever the assets return)
      const page = await env.ASSETS.fetch(new Request(new URL("/404", url), { method: "GET" }));
      const headers = new Headers(page.headers);
      if (!page.ok) headers.set("Content-Type", "text/plain; charset=utf-8");
      res = new Response(page.ok ? page.body : "Not found\n", { status: 404, headers });
    }
    return withHeaders(res, url.pathname);
  },
};
