// The portal's headers: the content security policy for each part of the site, the security
// headers every response carries and the cache policy. Kept out of worker.ts, whose module
// may export only its handler (the Workers runtime rejects any other export).

export const APEX = "maktaba.dev";

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

// The landing page, the docs, the changelog and the 404 page: no inline code at all.
export const CSP_DEFAULT = csp({});
// The API reference: Redoc (vendored, served from here) injects its styles and runs its
// search in a blob: worker.
export const CSP_API = csp({
  "style-src": "'self' 'unsafe-inline'",
  "font-src": "'self' data:",
  "worker-src": "'self' blob:",
});
export function cspFor(path: string): string {
  if (path.startsWith("/api/")) return CSP_API;
  return CSP_DEFAULT;
}

export const SECURITY_HEADERS: Record<string, string> = {
  // a year; no includeSubDomains or preload until every maktaba.dev host is HTTPS-only
  "Strict-Transport-Security": "max-age=31536000",
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "strict-origin-when-cross-origin",
  "Cross-Origin-Opener-Policy": "same-origin",
  "Permissions-Policy": "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
};

export const GITHUB = "https://github.com/Hamza-Labs-Core/Maktaba";

// Pages the site published before and does not any more. The epics wiki (/wiki/) and the
// Epic 54 story pages (/plugins/story-54-*) are not for this site: the specs live on
// GitHub, and the plugin guides replaced the story pages.
export const MOVED: [prefix: string, to: string][] = [
  ["/wiki", `${GITHUB}/tree/main/specs/epics`],
  ["/plugins/story-54-", "/plugins/"],
];

/** Where an old path now lives, or null. */
export function movedTo(path: string): string | null {
  for (const [prefix, to] of MOVED) {
    if (path === prefix || path.startsWith(prefix.endsWith("-") ? prefix : `${prefix}/`)) return to;
  }
  return null;
}

export function cacheControl(path: string, contentType: string, status: number): string {
  if (status >= 400) return "no-store";
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
