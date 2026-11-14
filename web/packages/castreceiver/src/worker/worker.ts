// cast.maktaba.tv (story 58.07): Maktaba's Cast receiver. Static assets only: /r/ (the loader,
// the URL registered in the Cast console), /r/builds.json and the builds at /r/<level>/. The
// apex sends people to maktaba.tv; anything else is a 404. No backend, no KV, no secrets, and
// no media: every video byte goes from the home or the relay straight to the TV.

import { withHeaders } from "./headers";

export interface Env {
  ASSETS: { fetch(req: Request): Promise<Response> };
}

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const url = new URL(req.url);
    const path = url.pathname;
    if (req.method !== "GET" && req.method !== "HEAD") {
      return withHeaders(
        new Response("Method not allowed\n", {
          status: 405,
          headers: { Allow: "GET, HEAD", "Content-Type": "text/plain; charset=utf-8" },
        }),
        path
      );
    }
    if (path === "/" || path === "") {
      return withHeaders(
        new Response(null, { status: 302, headers: { Location: "https://maktaba.tv/" } }),
        path
      );
    }
    if (path === "/r") {
      return withHeaders(
        new Response(null, { status: 301, headers: { Location: `/r/${url.search}` } }),
        path
      );
    }
    if (!path.startsWith("/r/")) {
      return withHeaders(
        new Response("Not found\n", {
          status: 404,
          headers: { "Content-Type": "text/plain; charset=utf-8" },
        }),
        path
      );
    }
    const res = await env.ASSETS.fetch(req);
    if (res.status === 404) {
      return withHeaders(
        new Response("Not found\n", {
          status: 404,
          headers: { "Content-Type": "text/plain; charset=utf-8" },
        }),
        path
      );
    }
    return withHeaders(res, path);
  },
};
