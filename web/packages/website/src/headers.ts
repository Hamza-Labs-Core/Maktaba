// maktaba.tv's headers (Epic 60 README §6.2): the content security policy for each part of
// the site, the security headers every response carries, and the cache policy. Kept out of
// worker.ts, whose module may export only its handler (the Workers runtime rejects any other
// export).

export const APEX = "maktaba.tv";

import table from "./redirects.json";

/** The Worker's redirects, source path → target path (src/redirects.json, which the link check
 * reads too). /docs goes to the help pages (story 60.12). */
export const REDIRECTS: Readonly<Record<string, string>> = Object.fromEntries(
  Object.entries(table).filter(([k]) => k.startsWith("/"))
);

/** Where /docs goes. */
export const DOCS_TARGET = REDIRECTS["/docs"];

const BASE_CSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-src 'none'",
  "frame-ancestors 'none'",
  "upgrade-insecure-requests",
];

/** Every page: nothing from another origin, no inline code, never in a frame. No path has a
 * policy of its own. */
export const CSP_DEFAULT = BASE_CSP.join("; ");

export const SECURITY_HEADERS: Record<string, string> = {
  // a year, and no includeSubDomains: the relay's names are DNS-only and must not inherit it
  "Strict-Transport-Security": "max-age=31536000",
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "strict-origin-when-cross-origin",
  "Cross-Origin-Opener-Policy": "same-origin",
  // everything the site does not use
  "Permissions-Policy":
    "accelerometer=(), autoplay=(), camera=(), display-capture=(), encrypted-media=(), " +
    "fullscreen=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), midi=(), " +
    "payment=(), picture-in-picture=(), publickey-credentials-get=(), screen-wake-lock=(), " +
    "serial=(), usb=(), xr-spatial-tracking=()",
};

// Vite's content-hashed names (index-Bx3k9Q2a.js) never change: cache them for good. The
// install build's go under /install/assets/ (web/vite.install.config.ts), the site's under
// /assets/.
const HASHED = /^\/(install\/)?assets\/.+-[A-Za-z0-9_-]{8,}\.(js|css|woff2?|svg|png|webp|avif)$/;

// The release feed (/updates/v1/<channel>/<platform>.json and its signature, docs/updates.md
// "How a home finds a release"): signed files that change with each release or pull, read by
// every home every 6 hours. A minute in any cache, so a release or a pull reaches homes at once.
const FEED = /^\/updates\/v\d+\//;

export function cacheControl(path: string, contentType: string, status: number): string {
  if (status >= 400) return "no-store";
  if (FEED.test(path)) return "public, max-age=60, must-revalidate";
  if (HASHED.test(path)) return "public, max-age=31536000, immutable";
  if (contentType.startsWith("text/html")) return "public, max-age=0, must-revalidate";
  return "public, max-age=3600";
}

export function redirect(to: string, status = 301): Response {
  return withHeaders(new Response(null, { status, headers: { Location: to } }), "/");
}

export function withHeaders(res: Response, path: string): Response {
  const out = new Response(res.body, res);
  for (const [k, v] of Object.entries(SECURITY_HEADERS)) out.headers.set(k, v);
  out.headers.set("Content-Security-Policy", CSP_DEFAULT);
  out.headers.set("X-Frame-Options", "DENY");
  if (res.status < 300 || res.status >= 400) {
    const type = out.headers.get("Content-Type") ?? "";
    out.headers.set("Cache-Control", cacheControl(path, type, res.status));
  }
  return out;
}
