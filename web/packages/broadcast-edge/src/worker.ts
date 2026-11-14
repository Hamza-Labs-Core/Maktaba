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

export { ChannelLive } from "./channel_live";
export { PublisherControl } from "./publisher";
export { Revocations } from "./revocations";
export { ViewerAccess } from "./access";
export { Pairing } from "./pairing";

export type Host = "ingest" | "live" | "watch";

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
      switch (hostOf(env, url)) {
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
} satisfies ExportedHandler<Env>;
