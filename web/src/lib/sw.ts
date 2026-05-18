// Service-worker cache lifecycle helpers (Story 11.10 hardening).
//
// The SW (public/sw.js) no longer caches any `/api/*` response, but
// older deployed SW versions populated a URL-keyed `mkt-api-*` Cache
// Storage with authenticated, user-scoped bodies. On logout we must
// scrub any such cache so the next user on a shared browser can never
// be served the previous user's library / saved searches / jobs.
//
// We do this two ways for defence in depth:
//   1. Delete every `mkt-api-*` cache directly from the page (works
//      even if no SW currently controls the page).
//   2. Post PURGE_API_CACHE to the active SW so a controlling SW that
//      still owns the cache also drops it.

const API_CACHE_PREFIX = "mkt-api-";

/** Delete every Cache Storage entry that holds API responses. */
async function deleteApiCaches(): Promise<void> {
  if (typeof caches === "undefined") return;
  try {
    const keys = await caches.keys();
    await Promise.all(
      keys.filter((k) => k.startsWith(API_CACHE_PREFIX)).map((k) => caches.delete(k))
    );
  } catch (err) {
    console.warn("sw: failed to delete API caches", err);
  }
}

/** Ask the controlling service worker (if any) to purge its API cache. */
function postPurgeToServiceWorker(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  navigator.serviceWorker.controller?.postMessage({ type: "PURGE_API_CACHE" });
}

/**
 * Purge all cached authenticated API responses. Call this on every
 * logout path before clearing local auth state. Best-effort and never
 * throws — a failure here must not block the user signing out.
 */
export async function purgeApiCacheOnLogout(): Promise<void> {
  postPurgeToServiceWorker();
  await deleteApiCaches();
}
