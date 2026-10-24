// PWA service-worker registration (Story 11.10).
//
// Registered AFTER first paint (on `load`) so the SW install never
// competes with the initial render. Dev (vite) skips registration so
// HMR is not shadowed by a cached shell.
//
// A new SW deliberately stays in the "waiting" state (sw.js no longer
// calls skipWaiting() on install). When one is detected we dispatch the
// `mkt:sw-update` CustomEvent carrying the registration; the app shell
// shows a toast with a real "Reload" action. Accepting it posts
// SKIP_WAITING to the waiting worker; the resulting `controllerchange`
// reloads the page exactly once so the new shell takes over.
export interface SwUpdateDetail {
  registration: ServiceWorkerRegistration;
}

export function applyServiceWorkerUpdate(reg: ServiceWorkerRegistration): void {
  const waiting = reg.waiting;
  if (!waiting) {
    // Nothing waiting (already activated, or a transient race): just
    // reload to pick up whatever is current.
    window.location.reload();
    return;
  }
  waiting.postMessage({ type: "SKIP_WAITING" });
}

export function registerServiceWorker(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  if (import.meta.env?.DEV) return;

  let reloading = false;
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    // Fires when the waiting SW we activated takes control. Guard so a
    // browser that fires this more than once cannot reload-loop.
    if (reloading) return;
    reloading = true;
    window.location.reload();
  });

  window.addEventListener("load", () => {
    navigator.serviceWorker
      .register("/sw.js")
      .then((reg) => {
        const emitIfWaiting = () => {
          if (!reg.waiting) return;
          window.dispatchEvent(
            new CustomEvent<SwUpdateDetail>("mkt:sw-update", {
              detail: { registration: reg },
            })
          );
        };
        // A new SW may already be installed and waiting at register().
        emitIfWaiting();
        reg.addEventListener("updatefound", () => {
          const installing = reg.installing;
          if (!installing) return;
          installing.addEventListener("statechange", () => {
            if (installing.state === "installed" && navigator.serviceWorker.controller) {
              emitIfWaiting();
            }
          });
        });
      })
      .catch((err) => {
        console.warn("pwa: service worker registration failed", err);
      });
  });
}

/** Where the last reload for a missing chunk is remembered (one per minute at most). */
const STALE_RELOAD_KEY = "mkt:stale-chunk-reload";

/**
 * A page an older release left open asks for its own hashed chunks, which the updated
 * server no longer has (a 404): Vite reports `vite:preloadError`. The page reloads once
 * to take the new release's page and chunks; a second failure within a minute is left to
 * show (no reload loop).
 */
export function reloadOnStaleChunks(
  win: Pick<Window, "addEventListener" | "location"> = window,
  store: Pick<Storage, "getItem" | "setItem"> | null = safeSession(),
  now: () => number = Date.now
): void {
  win.addEventListener("vite:preloadError", (event: Event) => {
    const last = Number(store?.getItem(STALE_RELOAD_KEY) ?? 0);
    if (now() - last < 60_000) return;
    try {
      store?.setItem(STALE_RELOAD_KEY, String(now()));
    } catch {
      // storage refused (a private window): reload anyway, once per page
    }
    event.preventDefault();
    win.location.reload();
  });
}

function safeSession(): Storage | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}
