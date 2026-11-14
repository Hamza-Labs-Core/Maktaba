// The broadcast edge's bindings (story 50.21 §1). Secrets are set with `wrangler secret put`,
// never written in wrangler.toml or the repository (docs/broadcast-edge.md lists them).
import type { ChannelLive } from "./channel_live";
import type { PublisherControl } from "./publisher";
import type { Revocations } from "./revocations";
import type { ViewerAccess } from "./access";
import type { Pairing } from "./pairing";

/** A Workers Rate Limiting binding. */
export type RateLimit = { limit(opts: { key: string }): Promise<{ success: boolean }> };

export interface Env {
  /** R2 bucket maktaba-broadcast-<env>: segments (10 minutes) and slates. */
  SEGMENTS: R2Bucket;
  /** R2 bucket for guide blobs (34.2 format), served on watch.maktaba.tv (50.26). */
  GUIDE?: R2Bucket;
  CHANNEL: DurableObjectNamespace<ChannelLive>;
  PUBLISHER: DurableObjectNamespace<PublisherControl>;
  REVOCATIONS: DurableObjectNamespace<Revocations>;
  /** One per publisher: invites' uses, guest passes, viewer sessions and their limits (50.26). */
  VIEWER: DurableObjectNamespace<ViewerAccess>;
  /** One per TV pairing nonce: the credential a phone sends a TV, for 10 minutes (50.27). */
  PAIRING: DurableObjectNamespace<Pairing>;
  /** watch.maktaba.tv's play requests: 30 a minute per address (50.26 §4). */
  PLAY_RL?: RateLimit;
  /** watch.maktaba.tv's other API requests: 120 a minute per address. */
  API_RL?: RateLimit;
  /** Turnstile's public site key for the watch pages' widget (50.27); unset: no widget. */
  TURNSTILE_SITE_KEY?: string;
  /** "1": the watch pages say "temporarily unavailable"; the API and playback stay up. */
  WATCH_SITE_OFF?: string;
  /** The Turnstile secret (open access, 50.26 §2); unset: open sessions answer 503. */
  TURNSTILE_SECRET?: string;
  /** Turnstile's siteverify (a fake service binding in tests; absent: challenges.cloudflare.com). */
  TURNSTILE_API?: Fetcher;
  /**
   * The native apps' attestation verifier (App Attest, Play Integrity; 50.27): POST /verify
   * {platform, attestation} answers {valid}. Absent: the exchange answers 503.
   */
  ATTEST_API?: Fetcher;
  /** Analytics Engine dataset broadcast_events (50.28). Optional: absent in dev. */
  AE?: AnalyticsEngineDataset;
  /** A coarse per-location guard in front of the exact per-rung one in ChannelLive. */
  RL?: { limit(opts: { key: string }): Promise<{ success: boolean }> };
  /** The Stream API (a service binding in tests; absent in production: api.cloudflare.com). */
  STREAM_API?: Fetcher;

  /** "staging" | "production": the first part of every R2 key. */
  BROADCAST_ENV: string;
  /** JSON {"<kid>": "<base64url raw Ed25519 public key>"}: the cloud's ingest and control keys. */
  CLOUD_PUBKEYS: string;
  /** HMAC key (base64url, ≥ 32 bytes) of viewer tokens, shared with watch.maktaba.tv (50.26). */
  VIEWER_TOKEN_KEY: string;
  /** base64url Ed25519 seed: the edge's signature on its posts to api.maktaba.tv (50.20 usage). */
  EDGE_SIGNING_KEY?: string;
  /** "1" stops every broadcast at the edge (break-glass, 50.25). */
  BROADCAST_GLOBAL_OFF?: string;
  /** "r2" (default) | "stream": the delivery when a publisher has no override (§5, Q8). */
  BROADCAST_DELIVERY?: string;
  /** The API the edge reports usage to (50.20); unset: nothing is posted. */
  API_ORIGIN?: string;
  INGEST_HOST?: string;
  LIVE_HOST?: string;
  WATCH_HOST?: string;
  /** Comma-separated origins allowed to read playlists and segments (CORS). */
  CORS_ORIGINS?: string;

  // The Stream fallback (§5): Maktaba's account, never a home's
  STREAM_ACCOUNT_ID?: string;
  STREAM_API_TOKEN?: string;
  STREAM_CUSTOMER_CODE?: string;
  STREAM_KEY_ID?: string;
  STREAM_KEY_JWK?: string;
}

export const DEFAULT_INGEST_HOST = "ingest.maktaba.tv";
export const DEFAULT_LIVE_HOST = "live.maktaba.tv";
export const DEFAULT_WATCH_HOST = "watch.maktaba.tv";

export type Delivery = "r2" | "stream";

/** globalDelivery is BROADCAST_DELIVERY, read strictly: anything but "stream" is "r2". */
export function globalDelivery(env: Env): Delivery {
  return env.BROADCAST_DELIVERY === "stream" ? "stream" : "r2";
}

export function globallyOff(env: Env): boolean {
  return env.BROADCAST_GLOBAL_OFF === "1" || env.BROADCAST_GLOBAL_OFF === "true";
}
