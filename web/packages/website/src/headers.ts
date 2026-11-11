// maktaba.tv's headers (Epic 60 README §6.2): the content security policy for each part of
// the site, the security headers every response carries, and the cache policy. Kept out of
// worker.ts, whose module may export only its handler (the Workers runtime rejects any other
// export).

export const APEX = "maktaba.tv";

/** Where /docs goes: the front door until the help pages exist (60.12 makes it /help/). */
export const DOCS_TARGET = "/";

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

// the base policy with some directives replaced, and any new ones added
function csp(over: Record<string, string>): string {
  const names = BASE_CSP.map((d) => d.split(" ")[0]);
  const base = BASE_CSP.map((d, i) => (names[i] in over ? `${names[i]} ${over[names[i]]}` : d));
  const added = Object.entries(over)
    .filter(([k]) => !names.includes(k))
    .map(([k, v]) => `${k} ${v}`);
  return [...base, ...added].join("; ");
}

/** Every page: nothing from another origin, no inline code, never in a frame. */
export const CSP_DEFAULT = csp({});

/** The copyright page only (60.07): its notice form needs Turnstile and posts to the cloud
 * API. No other path gets either origin. */
export const CSP_COPYRIGHT = csp({
  "script-src": "'self' https://challenges.cloudflare.com",
  "frame-src": "https://challenges.cloudflare.com",
  "connect-src": "'self' https://api.maktaba.tv",
});

const COPYRIGHT = /^(\/ar)?\/legal\/copyright\/?$/;

export function cspFor(path: string): string {
  return COPYRIGHT.test(path) ? CSP_COPYRIGHT : CSP_DEFAULT;
}

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

export function cacheControl(path: string, contentType: string, status: number): string {
  if (status >= 400) return "no-store";
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
  out.headers.set("Content-Security-Policy", cspFor(path));
  out.headers.set("X-Frame-Options", "DENY");
  if (res.status < 300 || res.status >= 400) {
    const type = out.headers.get("Content-Type") ?? "";
    out.headers.set("Cache-Control", cacheControl(path, type, res.status));
  }
  return out;
}
