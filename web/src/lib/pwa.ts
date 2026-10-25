// PWA service-worker registration (Story 11.10).
//
// Registered AFTER first paint (on `load`) so the SW install never
// competes with the initial render. Dev (vite) skips registration so
// HMR is not shadowed by a cached shell.
//
// A new SW takes over at once (sw.js calls skipWaiting() on install: a
// worker left waiting behind a stale shell could never be accepted, since
// that shell cannot render the toast). Each release is a new worker (its
// build stamp), so a takeover is the sign of an update: when the page is
// older than the server's release (pageIsStale), we dispatch the
// `mkt:sw-update` CustomEvent and the app shell shows a toast with a real
// "Reload" action. The page is never reloaded behind the user's back (a
// video playing, a form half filled), and a page that is the current
// release already (it loaded network-first just now) is not offered one.
export interface SwUpdateDetail {
  registration?: ServiceWorkerRegistration;
}

/** Set when the user accepted an update a worker still waiting carries: its takeover reloads. */
let accepted = false;

export function applyServiceWorkerUpdate(reg?: ServiceWorkerRegistration): void {
  const waiting = reg?.waiting;
  if (!waiting) {
    // The new worker controls the page already: reload onto the new release.
    window.location.reload();
    return;
  }
  // A worker still waiting: it takes over first, and its takeover reloads (watchTakeovers).
  accepted = true;
  waiting.postMessage({ type: "SKIP_WAITING" });
}

/** The page's entry script (Vite's `<script type="module" src="/assets/index-….js">`). */
const ENTRY = 'script[type="module"][src*="/assets/"]';

function entryOf(doc: ParentNode): string | null {
  return doc.querySelector(ENTRY)?.getAttribute("src") ?? null;
}

/**
 * Whether this page is an older release than the server's: the entry script the server's
 * page names now (fetched past every cache) is not the one this page runs. False when
 * either is not known (offline, a page without one).
 */
export async function pageIsStale(
  doc: ParentNode = document,
  fetchPage: () => Promise<Response> = () =>
    fetch("/", { cache: "no-store", credentials: "same-origin" })
): Promise<boolean> {
  const own = entryOf(doc);
  if (!own) return false;
  try {
    const res = await fetchPage();
    if (!res.ok) return false;
    const now = entryOf(new DOMParser().parseFromString(await res.text(), "text/html"));
    return now !== null && now !== own;
  } catch {
    return false; // offline: nothing to offer
  }
}

/**
 * What a worker taking control of the page means (`controllerchange`): the takeover the
 * user accepted reloads, once; any other offers the reload (mkt:sw-update) when the page
 * is older than the server's release (a first visit's worker, or an update that this page
 * already runs, says nothing).
 */
export function watchTakeovers(
  sw: Pick<ServiceWorkerContainer, "addEventListener">,
  registration: () => ServiceWorkerRegistration | undefined,
  win: Pick<Window, "dispatchEvent" | "location"> = window,
  stale: () => Promise<boolean> = () => pageIsStale()
): void {
  let reloading = false;
  sw.addEventListener("controllerchange", () => {
    if (accepted) {
      // Guard so a browser that fires this more than once cannot reload-loop.
      if (reloading) return;
      reloading = true;
      win.location.reload();
      return;
    }
    void stale().then((old) => {
      if (!old) return;
      win.dispatchEvent(
        new CustomEvent<SwUpdateDetail>("mkt:sw-update", {
          detail: { registration: registration() },
        })
      );
    });
  });
}

export function registerServiceWorker(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  if (import.meta.env?.DEV) return;

  let registration: ServiceWorkerRegistration | undefined;
  watchTakeovers(navigator.serviceWorker, () => registration);

  window.addEventListener("load", () => {
    navigator.serviceWorker
      .register("/sw.js")
      .then((reg) => {
        registration = reg;
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
