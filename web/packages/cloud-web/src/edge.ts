// The cloud's two web Workers (Epic 50 story 50.32 §3, §5): maktaba-cloud-web on app.maktaba.tv
// and maktaba-admin-web on admin.maktaba.tv. Each sits in front of its bundle's static assets
// (run_worker_first, so every response passes here) and does three things:
//   - passes the API paths its host may reach to the origin, unchanged (fetch(request): the
//     hostname's DNS record is the Tunnel's proxied CNAME, so this reaches the api role);
//   - answers every other API path 404 itself, so a path that belongs to the other host never
//     reaches the origin from this one;
//   - serves the bundle, with the SPA's index.html for an app route;
// and sets the host's headers (§5) on all of it, assets and API answers alike.
// Kept out of worker.ts, whose module may export only its handler.

export interface Assets {
  fetch(req: Request): Promise<Response>;
}

export interface Env {
  ASSETS: Assets;
}

/** How the Worker reaches the origin; the runtime's fetch, a stub in the tests. */
export type Origin = (req: Request) => Promise<Response>;

export interface Site {
  /** The host's name, for the log of a refused path. */
  host: string;
  /** Whether a request for this /v1/… path (and method) goes to the origin. */
  passes(method: string, path: string): boolean;
  /** Whether a non-/v1 path goes to the origin (/.well-known/*, /healthz). */
  passesOther(path: string): boolean;
  /** The host's headers on every response. */
  headers: Record<string, string>;
  /** Extra headers on HTML and /v1 answers (the console's no-store). */
  noStore: boolean;
  /** Files the Worker writes itself (robots.txt). */
  files?: Record<string, { body: string; type: string }>;
}

/** The paths of the operator console's API: on the user host they are 404 (50.32 §3). */
export function isStaffPath(path: string): boolean {
  return (
    path === "/v1/admin" ||
    path === "/v1/staff" ||
    path.startsWith("/v1/admin/") ||
    path.startsWith("/v1/staff/")
  );
}

// HSTS as 50.30 §6 sets it: a year, no includeSubDomains or preload yet.
const HSTS = "max-age=31536000";

/** The headers every cloud host sends (50.32 §5), with the host's CSP. */
export function hostHeaders(
  csp: string,
  extra: Record<string, string> = {}
): Record<string, string> {
  return {
    "Content-Security-Policy": csp,
    "Strict-Transport-Security": HSTS,
    "X-Content-Type-Options": "nosniff",
    "X-Frame-Options": "DENY",
    ...extra,
  };
}

function notFoundJSON(): Response {
  return new Response(JSON.stringify({ error: "not_found", message: "Not found." }), {
    status: 404,
    headers: { "Content-Type": "application/json" },
  });
}

// an app route: no file extension in its last segment (/accounts/42, not /assets/x.js)
const isAppRoute = (path: string) => !/\.[a-z0-9]+$/i.test(path.split("/").pop() ?? "");

function withHeaders(res: Response, site: Site, path: string): Response {
  const out = new Response(res.body, res);
  for (const [k, v] of Object.entries(site.headers)) out.headers.set(k, v);
  const html = (out.headers.get("Content-Type") ?? "").startsWith("text/html");
  if (site.noStore && (html || path.startsWith("/v1/"))) {
    out.headers.set("Cache-Control", "no-store");
  } else if (path.startsWith("/assets/") && out.status === 200) {
    // Vite's hashed file names: a new build has new names
    out.headers.set("Cache-Control", "public, max-age=31536000, immutable");
  } else if (html) {
    out.headers.set("Cache-Control", "no-cache");
  }
  return out;
}

/** The Worker's handler for site; origin is how it reaches the api (the runtime's fetch). */
export function handle(site: Site, origin: Origin) {
  return async (req: Request, env: Env): Promise<Response> => {
    const url = new URL(req.url);
    const path = url.pathname;
    if (path === "/v1" || path.startsWith("/v1/")) {
      const res = site.passes(req.method, path) ? await origin(req) : notFoundJSON();
      return withHeaders(res, site, path);
    }
    if (site.passesOther(path)) return withHeaders(await origin(req), site, path);

    const own = site.files?.[path];
    if (own) {
      return withHeaders(
        new Response(own.body, { headers: { "Content-Type": own.type } }),
        site,
        path
      );
    }
    if (req.method !== "GET" && req.method !== "HEAD") {
      const res = new Response("Method not allowed\n", {
        status: 405,
        headers: { Allow: "GET, HEAD", "Content-Type": "text/plain; charset=utf-8" },
      });
      return withHeaders(res, site, path);
    }
    let res = await env.ASSETS.fetch(req);
    if (res.status === 404 && isAppRoute(path)) {
      // the SPA: its router draws the page, its own 404 included
      res = await env.ASSETS.fetch(new Request(new URL("/index.html", url), { method: "GET" }));
    }
    return withHeaders(res, site, path);
  };
}
