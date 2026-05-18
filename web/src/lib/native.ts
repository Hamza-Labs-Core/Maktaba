// Native-runtime detection + lifecycle consumers (Story 12.1 / 12.2).
//
// Background: apps/mobile/src/native-shell.ts is a Capacitor lifecycle
// bridge that publishes `mkt:*` DOM events. The gap analysis found it is
// dead code — its documented importer (this file) did not exist, so
// `installNativeShell()` was never called and the ~7 "unwired" ACs
// (background-pause, low-memory cache drop, WS throttle, status-bar
// theming) had no web-side consumer.
//
// This file closes that wiring:
//
//   1. `isNativeRuntime()` — runtime Capacitor detection. The web bundle
//      MUST stay free of any `@capacitor/*` static import (those deps
//      live in apps/mobile, not web/package.json) so the production
//      build and typecheck do not require the native toolchain.
//   2. `installNativeConsumers()` — attaches the web-side listeners for
//      the bridge's events. These are pure DOM/Cache-API consumers and
//      run on every platform; they are no-ops until the bridge fires.
//   3. `bootNative()` — invoked from main.tsx. Installs the consumers
//      always (cheap, idempotent) and, ONLY inside the Capacitor shell,
//      dynamically imports + runs the native bridge via a runtime
//      module path the web build never resolves.
//
// Splitting "consumers" (web, always-on, testable) from "bridge"
// (native, dynamically loaded) is what keeps `pnpm build` / `typecheck`
// green without pulling Capacitor into the web dependency graph.

interface CapacitorGlobal {
  isNativePlatform?: () => boolean;
}

// Story 12.9 deep-link routes. A `maktaba://` custom-scheme URL or an
// https Universal/App Link both reduce to one of these SPA paths.
const DEEP_LINK_ROUTES = new Set(["watch", "search", "library", "queue", "settings", "collection"]);

/**
 * Map a deep-link URL to an in-app path (Story 12.9). Pure + exported so
 * it is unit-testable without a Capacitor runtime. Returns null for
 * unknown/foreign URLs so the caller can fall back to the web route or
 * ignore it.
 *
 *   maktaba://watch/123?t=42        → /watch/123?t=42
 *   https://maktaba.app/library/abc → /library/abc
 *   https://example.com/watch/1     → null  (foreign host, custom only)
 */
export function deepLinkToPath(raw: string): string | null {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return null;
  }
  const isCustom = u.protocol === "maktaba:";
  const base = isCustom ? `${u.host}${u.pathname}` : u.pathname;
  const segs = base.split("/").filter(Boolean);
  if (segs.length === 0 || !DEEP_LINK_ROUTES.has(segs[0])) return null;
  return "/" + segs.join("/") + (u.search ?? "");
}

/** True only when running inside the Capacitor native shell. */
export function isNativeRuntime(): boolean {
  const cap = (window as unknown as { Capacitor?: CapacitorGlobal }).Capacitor;
  return typeof cap?.isNativePlatform === "function" ? cap.isNativePlatform() : false;
}

// Cache names the SPA may drop under memory pressure. Keep in sync with
// the service-worker / runtime cache keys; unknown caches are left
// untouched so a low-memory signal never nukes unrelated storage.
const DROPPABLE_CACHES = new Set(["mkt-thumbs", "mkt-api"]);

// 60 s — Story 12.1 AC: "Background pauses timers; WS throttle 60 s".
const WS_THROTTLE_MS = 60_000;

let installed = false;
let throttleTimer: ReturnType<typeof setTimeout> | null = null;
let attached: Array<[string, EventListener]> = [];

/**
 * Attach the web-side consumers for the native bridge's DOM events.
 * Idempotent and platform-agnostic: safe to call on web (the events
 * simply never fire there).
 */
export function installNativeConsumers(): void {
  if (installed) return;
  installed = true;

  const onBackground = () => {
    document.documentElement.dataset.mktAppState = "background";
    if (throttleTimer) clearTimeout(throttleTimer);
    // Defer the WS throttle so a quick app-switch (background→resume
    // within 60 s) does not needlessly tear down the socket.
    throttleTimer = setTimeout(() => {
      window.dispatchEvent(new CustomEvent("mkt:wsThrottle"));
      throttleTimer = null;
    }, WS_THROTTLE_MS);
  };

  const onResume = () => {
    document.documentElement.dataset.mktAppState = "active";
    if (throttleTimer) {
      clearTimeout(throttleTimer);
      throttleTimer = null;
    }
  };

  const onLowMemory = () => {
    void dropCaches();
  };

  // Story 12.9: a deep link arriving (incl. cold launch, since the
  // bridge replays the launch URL through the same event) is turned
  // into an SPA navigation. The pending link is also stashed so the
  // login flow can resume it post-auth ("preserve deep link across
  // login" AC).
  const onDeepLink = (e: Event) => {
    const detail = (e as CustomEvent).detail as { path?: string; raw?: string } | undefined;
    const path = detail?.path ?? (detail?.raw ? deepLinkToPath(detail.raw) : null) ?? null;
    if (!path) return;
    pendingDeepLink = path;
    window.dispatchEvent(new CustomEvent("mkt:navigate", { detail: { path } }));
  };

  attached = [
    ["mkt:appBackgrounded", onBackground as EventListener],
    ["mkt:appResumed", onResume as EventListener],
    ["mkt:lowMemory", onLowMemory as EventListener],
    ["mkt:deepLink", onDeepLink as EventListener],
  ];
  for (const [name, fn] of attached) window.addEventListener(name, fn);
}

let pendingDeepLink: string | null = null;

/**
 * Story 12.9 "preserve deep link across login": the auth flow calls
 * this after a successful login to resume the route the user deep-linked
 * into before being bounced to /login. Returns and clears the pending
 * path (null if none).
 */
export function consumePendingDeepLink(): string | null {
  const p = pendingDeepLink;
  pendingDeepLink = null;
  return p;
}

async function dropCaches(): Promise<void> {
  const c = (globalThis as unknown as { caches?: CacheStorage }).caches;
  if (!c) return;
  try {
    const keys = await c.keys();
    await Promise.all(keys.filter((k) => DROPPABLE_CACHES.has(k)).map((k) => c.delete(k)));
  } catch {
    // Cache API absent or denied — best-effort, never throw into the
    // event loop.
  }
}

/**
 * Entry point called from main.tsx. Always installs the (cheap, web)
 * consumers; additionally boots the Capacitor bridge when — and only
 * when — running inside the native shell.
 *
 * The bridge import is a runtime-evaluated specifier so the web bundler
 * never tries to resolve `@capacitor/*` (those packages are not in
 * web/package.json). In the native build apps/mobile aliases this path
 * to native-shell.ts.
 */
export async function bootNative(): Promise<void> {
  installNativeConsumers();
  if (!isNativeRuntime()) return;
  try {
    const spec = "maktaba-native-shell";
    const mod = (await import(/* @vite-ignore */ spec)) as {
      installNativeShell?: () => Promise<void>;
    };
    await mod.installNativeShell?.();
  } catch {
    // No bridge bound (e.g. detection false-positive in a hybrid
    // preview) — the web consumers above still provide graceful
    // degradation.
  }
}

/** Test-only: reset module state between cases. */
export function __resetNativeConsumersForTest(): void {
  for (const [name, fn] of attached) window.removeEventListener(name, fn);
  attached = [];
  installed = false;
  pendingDeepLink = null;
  if (throttleTimer) {
    clearTimeout(throttleTimer);
    throttleTimer = null;
  }
  delete document.documentElement.dataset.mktAppState;
}
