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
import { handleIngest } from "./ingest";
import { handleLive } from "./live";
import { log } from "./log";

export { ChannelLive } from "./channel_live";
export { PublisherControl } from "./publisher";
export { Revocations } from "./revocations";

export type Host = "ingest" | "live" | "watch";

/**
 * hostOf picks the edge's role from the hostname. Any other host (wrangler dev on localhost, the
 * CI rehearsal) is routed by path: /v1/… is ingest, everything else live.
 */
export function hostOf(env: Env, url: URL): Host {
  const h = url.hostname;
  if (h === (env.INGEST_HOST || DEFAULT_INGEST_HOST)) return "ingest";
  if (h === (env.LIVE_HOST || DEFAULT_LIVE_HOST)) return "live";
  if (h === (env.WATCH_HOST || DEFAULT_WATCH_HOST)) return "watch";
  return url.pathname.startsWith("/v1/") ? "ingest" : "live";
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    try {
      switch (hostOf(env, url)) {
        case "ingest":
          return await handleIngest(request, env, url);
        case "live":
          return await handleLive(request, env, url);
        case "watch":
          // the site, its API and the guide blobs arrive with 50.26 and 50.27
          return problem(404, "not_found");
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
