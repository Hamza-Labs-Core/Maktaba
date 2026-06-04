// Native lifecycle bridge for the Capacitor shell (Story 12.1 / 12.2).
//
// The shared SPA imports this when Capacitor is detected at runtime —
// see web/src/lib/native.ts. The bridge publishes DOM events the SPA
// already listens for, so the same React code drives the web and the
// native experience without compile-time branching.
import { App, type AppState, type URLOpenListenerEvent } from '@capacitor/app';
import { Network, type ConnectionStatus } from '@capacitor/network';
import { StatusBar, Style } from '@capacitor/status-bar';
import { SplashScreen } from '@capacitor/splash-screen';
import { Preferences } from '@capacitor/preferences';
import { SecureStorage } from '@aparajita/capacitor-secure-storage';
import {
  Haptics,
  ImpactStyle,
  NotificationType,
} from '@capacitor/haptics';

const dispatch = (name: string, detail?: unknown) => {
  window.dispatchEvent(new CustomEvent(name, { detail }));
};

/**
 * Parse a `maktaba://` (or https Universal/App-Link) URL into a SPA
 * path. Story 12.9: scheme `maktaba://watch/{id}?t=` and the
 * /watch /search /library /queue /settings /collection routes.
 *
 * Exported (and pure) so the web side can unit-test the mapping without
 * a Capacitor runtime.
 */
export function deepLinkToPath(raw: string): string | null {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return null;
  }
  // maktaba://watch/123 → host="watch", pathname="/123"
  // https://maktaba.app/watch/123 → pathname="/watch/123"
  const isCustom = u.protocol === 'maktaba:';
  const segs = (
    isCustom ? `${u.host}${u.pathname}` : u.pathname
  )
    .split('/')
    .filter(Boolean);
  if (segs.length === 0) return null;
  const known = new Set([
    'watch',
    'search',
    'library',
    'queue',
    'settings',
    'collection',
  ]);
  if (!known.has(segs[0])) return null;
  const qs = u.search ?? '';
  return '/' + segs.join('/') + qs;
}

/**
 * Wire native lifecycle → DOM events. Idempotent: safe to call once at
 * boot.
 */
export async function installNativeShell(): Promise<void> {
  App.addListener('appStateChange', (state: AppState) => {
    dispatch(state.isActive ? 'mkt:appResumed' : 'mkt:appBackgrounded');
  });
  App.addListener('appRestoredResult', (data) => {
    dispatch('mkt:appRestored', data);
  });

  // Story 12.9: deep links (custom scheme + Universal/App Links). The
  // SPA router consumes `mkt:deepLink` and navigates; cold-launch links
  // are delivered through the same event because Capacitor replays the
  // launch URL via appUrlOpen.
  App.addListener('appUrlOpen', (ev: URLOpenListenerEvent) => {
    const path = deepLinkToPath(ev.url);
    if (path) dispatch('mkt:deepLink', { path, raw: ev.url });
  });

  // Story 12.2: Android hardware back button. Pop SPA history; at the
  // history root emit a quit-prompt request the SPA can confirm before
  // calling App.exitApp().
  App.addListener('backButton', ({ canGoBack }) => {
    if (canGoBack) {
      window.history.back();
    } else {
      dispatch('mkt:backAtRoot');
    }
  });

  Network.addListener('networkStatusChange', (status: ConnectionStatus) => {
    dispatch('mkt:networkChange', status);
  });

  // Memory-warning is iOS-specific; Capacitor exposes it via the App
  // plugin's `pause/resume`. The shell maps low-memory to a stronger
  // signal so the SPA can drop caches without unmounting the view.
  // (iOS native shim plumbs UIApplicationDidReceiveMemoryWarningNotification
  //  into a console.warn message; we forward it here.)
  window.addEventListener('lowmemory', () => dispatch('mkt:lowMemory'));

  // Match status-bar tint to the current data-theme; refresh on theme
  // change so a user toggle inside the SPA stays consistent.
  const applyStatusBar = async () => {
    const dark = document.documentElement.dataset.theme === 'dark';
    try {
      await StatusBar.setStyle({ style: dark ? Style.Dark : Style.Light });
    } catch {
      // setStyle is iOS/Android only; ignore on web preview.
    }
  };
  await applyStatusBar();
  new MutationObserver(applyStatusBar).observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-theme'],
  });

  // Hide the splash AFTER first paint so users never see a flash of
  // white while the SPA boots.
  setTimeout(() => SplashScreen.hide().catch(() => undefined), 0);
}

/**
 * Haptics (Story 12.8). Closed vocabulary mapped to UIImpact /
 * UINotification (iOS) and HapticFeedbackConstants (Android) by the
 * @capacitor/haptics plugin.
 *
 *   tap            — light impact   (tab change, button)
 *   medium         — medium impact  (long-press)
 *   selection      — selection tick (toggle, picker)
 *   success/warning/error — notification haptics (download done / EC)
 *
 * EC: throttled to at most one pulse / 100 ms. The OS reduce-motion /
 * reduced-haptics preference and the in-app Settings → Accessibility →
 * Haptics level (off | light | full) both gate firing.
 */
export type HapticKind =
  | 'tap'
  | 'medium'
  | 'selection'
  | 'success'
  | 'warning'
  | 'error';

export type HapticLevel = 'off' | 'light' | 'full';

let hapticLevel: HapticLevel = 'full';
let lastHapticAt = 0;

/** Set by the SPA from Settings → Accessibility → Haptics. */
export function setHapticLevel(level: HapticLevel): void {
  hapticLevel = level;
}

function prefersReducedMotion(): boolean {
  try {
    return (
      typeof matchMedia === 'function' &&
      matchMedia('(prefers-reduced-motion: reduce)').matches
    );
  } catch {
    return false;
  }
}

/** Decide (purely) whether a given haptic kind should fire right now. */
export function shouldFireHaptic(
  kind: HapticKind,
  now: number,
  level: HapticLevel = hapticLevel,
  reducedMotion: boolean = prefersReducedMotion(),
): boolean {
  if (level === 'off') return false;
  if (reducedMotion) return false;
  // "light" only allows the gentle tap/selection family; impact-heavy
  // and notification haptics need the full setting.
  if (level === 'light' && kind !== 'tap' && kind !== 'selection') {
    return false;
  }
  return now - lastHapticAt >= 100; // EC: ≤ 1 / 100 ms
}

export async function haptic(kind: HapticKind = 'tap'): Promise<void> {
  const now = Date.now();
  if (!shouldFireHaptic(kind, now)) return;
  lastHapticAt = now;
  try {
    switch (kind) {
      case 'medium':
        await Haptics.impact({ style: ImpactStyle.Medium });
        break;
      case 'selection':
        await Haptics.selectionStart();
        await Haptics.selectionEnd();
        break;
      case 'success':
        await Haptics.notification({ type: NotificationType.Success });
        break;
      case 'warning':
        await Haptics.notification({ type: NotificationType.Warning });
        break;
      case 'error':
        await Haptics.notification({ type: NotificationType.Error });
        break;
      case 'tap':
      default:
        await Haptics.impact({ style: ImpactStyle.Light });
    }
  } catch {
    // ignore on web preview
  }
}

/**
 * Back-compat shim for the original single-style export.
 * @deprecated use {@link haptic}.
 */
export async function tapHaptic(): Promise<void> {
  await haptic('tap');
}

/**
 * Persist a small NON-SECRET key/value to the native preferences store
 * (@capacitor/preferences → UserDefaults / SharedPreferences, NOT
 * encrypted). Use for UI prefs (theme, last library) only. Secrets
 * (auth tokens) must go through {@link nativeSecureStore} below.
 */
export const nativePrefs = {
  async get(key: string): Promise<string | null> {
    const { value } = await Preferences.get({ key });
    return value ?? null;
  },
  async set(key: string, value: string): Promise<void> {
    await Preferences.set({ key, value });
  },
  async remove(key: string): Promise<void> {
    await Preferences.remove({ key });
  },
};

/**
 * Hardware-backed secure storage (Story 12.4 / auth) — Keychain on iOS,
 * EncryptedSharedPreferences via the Android Keystore. The SPA's auth
 * bridge persists the refresh token here so it is never written to the
 * plaintext preferences store or the WebView's localStorage.
 *
 * `@aparajita/capacitor-secure-storage` returns typed values (string |
 * number | boolean | object | null); we constrain the surface to
 * strings to match the web localStorage-shaped contract the auth bridge
 * already targets, so the same calling code works on web and native.
 */
export const nativeSecureStore = {
  async get(key: string): Promise<string | null> {
    const v = await SecureStorage.get(key);
    return typeof v === 'string' ? v : v == null ? null : String(v);
  },
  async set(key: string, value: string): Promise<void> {
    await SecureStorage.set(key, value);
  },
  async remove(key: string): Promise<void> {
    await SecureStorage.remove(key);
  },
};
