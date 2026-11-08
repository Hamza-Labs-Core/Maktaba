// app.maktaba.tv (Epic 50 story 50.32 §3, §5): the user app's bundle (dist-cloud/), every /v1
// route but the operator console's, and /.well-known/*. A console path is 404 here: the user
// host never reaches it, whoever asks.
import { hostHeaders, isStaffPath, type Site } from "./edge";

// §5's policy for app.maktaba.tv: Turnstile's script and frame (the sign-up and sign-in bot
// check, 50.30), https avatar images, nothing else from outside.
export const APP_CSP = [
  "default-src 'self'",
  "script-src 'self' https://challenges.cloudflare.com",
  "frame-src https://challenges.cloudflare.com",
  "connect-src 'self'",
  "img-src 'self' data: https:",
  "style-src 'self'",
  "font-src 'self'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'self'",
  "frame-ancestors 'none'",
].join("; ");

export const APP_SITE: Site = {
  host: "app.maktaba.tv",
  passes: (_method, path) => !isStaffPath(path),
  passesOther: (path) => path.startsWith("/.well-known/"),
  headers: hostHeaders(APP_CSP),
  noStore: false,
};
