// Responses and headers shared by the edge's hosts.
import type { Env } from "./env";

export function json(
  status: number,
  body: unknown,
  headers: Record<string, string> = {}
): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", "Cache-Control": "no-store", ...headers },
  });
}

export function problem(
  status: number,
  error: string,
  headers: Record<string, string> = {}
): Response {
  return json(status, { error }, headers);
}

const DEFAULT_ORIGINS = [
  "https://watch.maktaba.tv",
  "https://app.maktaba.tv",
  "https://maktaba.tv",
  // the native apps' web views (Capacitor) on iOS and Android
  "capacitor://localhost",
  "https://localhost",
];

export function allowedOrigins(env: Env): string[] {
  const set = (env.CORS_ORIGINS ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  return set.length > 0 ? set : DEFAULT_ORIGINS;
}

/**
 * withLiveHeaders adds live.maktaba.tv's headers to a response: CORS for the watch and app
 * origins (added per request, never stored in the cache), Timing-Allow-Origin for the latency
 * probe (50.28), nosniff, and never a cookie.
 */
export function withLiveHeaders(res: Response, request: Request, env: Env): Response {
  const out = new Response(res.body, res);
  const origins = allowedOrigins(env);
  const origin = request.headers.get("Origin");
  if (origin && origins.includes(origin)) {
    out.headers.set("Access-Control-Allow-Origin", origin);
    out.headers.set("Access-Control-Expose-Headers", "X-Maktaba-Cache, Content-Length");
  }
  out.headers.append("Vary", "Origin");
  out.headers.set("Timing-Allow-Origin", origins.join(", "));
  out.headers.set("X-Content-Type-Options", "nosniff");
  out.headers.delete("Set-Cookie");
  return out;
}

export function preflight(request: Request, env: Env): Response {
  const res = new Response(null, {
    status: 204,
    headers: {
      "Access-Control-Allow-Methods": "GET, HEAD, POST, OPTIONS",
      "Access-Control-Allow-Headers": "Content-Type, Range",
      "Access-Control-Max-Age": "86400",
    },
  });
  return withLiveHeaders(res, request, env);
}
