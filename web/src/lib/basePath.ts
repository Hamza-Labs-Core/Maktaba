// Epic 42.9: where the SPA lives. The home serves it at its root; Maktaba Cloud serves the
// player and remote editions for a connected home under /play/ and /manage/ on the home's
// relay address (`pnpm build:player:cloud`, `build:remote:cloud`: Vite's --base). The API
// and the streams stay at that origin's root (/api, /stream/), which the relay sends to the
// home, so only the router and the app's own files move under the base.

/** The app's base path without its trailing slash: "" at the root, "/play" under the cloud. */
export const BASE = import.meta.env.BASE_URL.replace(/\/+$/, "");

/** The router's basename (none at the root). */
export const ROUTER_BASENAME = BASE || undefined;

/** Served by Maktaba Cloud for a connected home, under a base path. */
export const IN_CLOUD = BASE !== "";
