// PWA service-worker registration (Story 11.10).
//
// Registered AFTER first paint (on `load`) so the SW install never
// competes with the initial render. Dev (vite) skips registration so
// HMR is not shadowed by a cached shell. An updated SW surfaces a
// reload affordance via the `mkt:sw-update` CustomEvent on window,
// which the shell listens for and shows a Toast for.
export function registerServiceWorker(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  if (import.meta.env?.DEV) return;

  window.addEventListener("load", () => {
    navigator.serviceWorker
      .register("/sw.js")
      .then((reg) => {
        reg.addEventListener("updatefound", () => {
          const installing = reg.installing;
          if (!installing) return;
          installing.addEventListener("statechange", () => {
            if (installing.state === "installed" && navigator.serviceWorker.controller) {
              window.dispatchEvent(new CustomEvent("mkt:sw-update"));
            }
          });
        });
      })
      .catch((err) => {
        console.warn("pwa: service worker registration failed", err);
      });
  });
}
