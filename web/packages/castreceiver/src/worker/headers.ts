// cast.maktaba.tv's headers (story 58.07): the content security policy, the security headers
// and the cache policy. Kept out of worker.ts, whose module may export only its handler.
//
// The page runs Google's CAF SDK (www.gstatic.com) and the Shaka Player it loads
// (ajax.googleapis.com), and fetches from any home's HTTPS address or the relay: every https:
// origin for its calls, media and posters (a home's address is the household's own). Never
// plain http: a receiver page's media must be HTTPS.

const CSP = [
  "default-src 'self'",
  "script-src 'self' https://www.gstatic.com https://ajax.googleapis.com",
  // CAF's <cast-media-player> styles itself
  "style-src 'self' 'unsafe-inline' https://www.gstatic.com",
  "img-src 'self' https: data: blob:",
  "media-src 'self' https: blob: data:",
  "connect-src 'self' https:",
  "font-src 'self' https://fonts.gstatic.com",
  "worker-src 'self' blob:",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "upgrade-insecure-requests",
].join("; ");

export const SECURITY: Readonly<Record<string, string>> = {
  "Content-Security-Policy": CSP,
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "no-referrer",
  "Strict-Transport-Security": "max-age=31536000; includeSubDomains",
  "Permissions-Policy": "camera=(), microphone=(), geolocation=()",
};

/** The cache policy of a path: the loader and the manifest are read fresh (a deploy changes
 * which build a sender gets); a level's build may be redeployed, so a short cache; the fonts
 * and the mark never change under one name. */
export function cacheFor(path: string): string {
  if (
    path === "/r/" ||
    path === "/r/index.html" ||
    path === "/r/builds.json" ||
    path === "/r/loader.js"
  ) {
    return "no-cache";
  }
  if (path.startsWith("/r/fonts/") || path === "/r/mark.svg") return "public, max-age=86400";
  if (/^\/r\/\d+\//.test(path)) return "public, max-age=300";
  return "no-store";
}

/** The response with the security and cache headers. */
export function withHeaders(res: Response, path: string): Response {
  const headers = new Headers(res.headers);
  for (const [k, v] of Object.entries(SECURITY)) headers.set(k, v);
  if (res.ok) headers.set("Cache-Control", cacheFor(path));
  // the manifest and the builds are read by the page and by the next release
  if (path === "/r/builds.json") headers.set("Access-Control-Allow-Origin", "*");
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers });
}
