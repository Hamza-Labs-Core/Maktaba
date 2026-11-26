// The broadcast edge (Epic 50 story 50.21): one Worker on Maktaba's Cloudflare account behind
// three proxied Workers Custom Domains on maktaba.tv. It is infrastructure only: it stores and
// delivers the segments and the guide a home sends, and enforces access, limits and revocations.
// It never chooses, reorders, ranks or recommends content (README §14.6).
//
//   ingest.maktaba.tv   homes push segments, state and slates (ingest.ts)
//   live.maktaba.tv     players fetch playlists and segments (live.ts)
//   watch.maktaba.tv    the site and its API (50.26, 50.27); guide blobs
//
// docs/broadcast-edge.md describes the settings, the secrets and how to run it.
import { DEFAULT_INGEST_HOST, DEFAULT_LIVE_HOST, DEFAULT_WATCH_HOST, type Env } from "./env";
import { problem } from "./http";
import { handleAccessControl } from "./access";
import { handleIngest } from "./ingest";
import { handleLive } from "./live";
import { log } from "./log";
import { handleWatch } from "./play";
import { pullRevocations } from "./revocations";

export { ChannelLive } from "./channel_live";
export { PublisherControl } from "./publisher";
export { Revocations } from "./revocations";
export { ViewerAccess } from "./access";
export { Pairing } from "./pairing";

export type Host = "ingest" | "live" | "watch";

/** broadcastingOn is BROADCAST_ENABLED: off (the default), the edge takes and serves nothing. */
export function broadcastingOn(env: Env): boolean {
  return env.BROADCAST_ENABLED === "1" || env.BROADCAST_ENABLED === "true";
}

/**
 * openWhileOff is the one thing that answers while broadcasting is off: the cloud's signed
 * revocation list, which only ever takes access away (the cron pulls the same list). Uploads,
 * state, guides, the control socket, the speed test, plans and viewer access are refused, a
 * valid token or signature included.
 */
function openWhileOff(env: Env, url: URL): boolean {
  const ingestHost = (env.INGEST_HOST || DEFAULT_INGEST_HOST).toLowerCase();
  return url.hostname.toLowerCase() === ingestHost && url.pathname === "/v1/control/revocations";
}

/** The answer of everything else while broadcasting is off: nothing about any channel. */
function offResponse(): Response {
  return new Response("Not found\n", {
    status: 404,
    headers: {
      "Content-Type": "text/plain; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Robots-Tag": "noindex, nofollow",
    },
  });
}

/**
 * hostOf picks the edge's role from the hostname. Any other host (wrangler dev on localhost, the
 * CI rehearsal) is routed by path: /v1/… is ingest; /api/…, /i/…, /s/…, /pair and /ar/… or /en/…
 * watch (the pages, 50.27); everything else live.
 */
export function hostOf(env: Env, url: URL): Host {
  const h = url.hostname;
  if (h === (env.INGEST_HOST || DEFAULT_INGEST_HOST)) return "ingest";
  if (h === (env.LIVE_HOST || DEFAULT_LIVE_HOST)) return "live";
  if (h === (env.WATCH_HOST || DEFAULT_WATCH_HOST)) return "watch";
  if (url.pathname.startsWith("/v1/")) return "ingest";
  if (/^\/(api|i|s|ar|en)\//.test(url.pathname) || /^\/(pair|ar|en)?$/.test(url.pathname))
    return "watch";
  return "live";
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    try {
      const host = hostOf(env, url);
      // broadcasting is off (2026-11-24): checked before any route, token or stored state
      if (!broadcastingOn(env) && !openWhileOff(env, url)) return offResponse();
      switch (host) {
        case "ingest":
          // the viewer-access control posts (50.26) beside 50.21's
          if (url.pathname === "/v1/control/access" || url.pathname === "/v1/control/passes")
            return await handleAccessControl(request, env, url.pathname);
          return await handleIngest(request, env, url);
        case "live":
          return await handleLive(request, env, url);
        case "watch":
          // the viewer API and the invite page (50.26); the pages, guide and pairing (50.27)
          return await handleWatch(request, env, url);
      }
    } catch (err) {
      log("error", "request failed", {
        component: "worker",
        err,
        attrs: { path: url.pathname.split("/").slice(0, 3).join("/") },
      });
      return problem(500, "edge_error");
    }
  },

  /** The cron (every minute): the revocation list's pull, the backup to the cloud's push (50.25). */
  async scheduled(
    _controller: ScheduledController,
    env: Env,
    ctx: ExecutionContext
  ): Promise<void> {
    ctx.waitUntil(
      pullRevocations(env).then(
        () => undefined,
        (err: unknown) => log("warn", "revocation pull failed", { component: "revocations", err })
      )
    );
  },
} satisfies ExportedHandler<Env>;
