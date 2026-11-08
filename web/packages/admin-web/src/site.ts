// admin.maktaba.tv (Epic 50 story 50.32 §3, §5): the operator console's bundle (dist-admin/),
// its API (/v1/staff/*, /v1/admin/*) and /healthz; any other /v1 path is 404 here. Until staff
// sign-in (50.33) replaces them, the console signs in with four account routes, and those
// four only pass too (cloud/cmd/maktaba-cloud ConsoleSignIn, the same list).
import { hostHeaders, isStaffPath, type Site } from "../../cloud-web/src/edge";

export const ADMIN_CSP = [
  "default-src 'none'",
  "script-src 'self' https://challenges.cloudflare.com",
  "frame-src https://challenges.cloudflare.com",
  "connect-src 'self'",
  "img-src 'self' data:",
  "style-src 'self'",
  "font-src 'self'",
  "base-uri 'none'",
  "form-action 'self'",
  "frame-ancestors 'none'",
].join("; ");

/** The account routes the console signs in with until 50.33: "METHOD path". */
export const CONSOLE_SIGN_IN = new Set([
  "POST /v1/auth/login",
  "POST /v1/auth/refresh",
  "POST /v1/auth/logout",
  "GET /v1/account/me",
]);

export const ADMIN_SITE: Site = {
  host: "admin.maktaba.tv",
  passes: (method, path) => isStaffPath(path) || CONSOLE_SIGN_IN.has(`${method} ${path}`),
  passesOther: (path) => path === "/healthz",
  headers: hostHeaders(ADMIN_CSP, {
    "Referrer-Policy": "no-referrer",
    "Cross-Origin-Opener-Policy": "same-origin",
    "Cross-Origin-Resource-Policy": "same-origin",
    "Permissions-Policy":
      "camera=(), microphone=(), geolocation=(), publickey-credentials-get=(self), publickey-credentials-create=(self)",
    "X-Robots-Tag": "noindex, nofollow",
  }),
  noStore: true,
  files: {
    "/robots.txt": { body: "User-agent: *\nDisallow: /\n", type: "text/plain; charset=utf-8" },
  },
};
