// Native lifecycle bridge for the Capacitor shell (Story 12.1 / 12.2).
//
// The shared SPA imports this when Capacitor is detected at runtime —
// see web/src/lib/native.ts. The bridge publishes DOM events the SPA
// already listens for, so the same React code drives the web and the
// native experience without compile-time branching.
import { App, type AppState } from '@capacitor/app';
import { Network, type ConnectionStatus } from '@capacitor/network';
import { StatusBar, Style } from '@capacitor/status-bar';
import { SplashScreen } from '@capacitor/splash-screen';
import { Preferences } from '@capacitor/preferences';
import { Haptics, ImpactStyle } from '@capacitor/haptics';

const dispatch = (name: string, detail?: unknown) => {
  window.dispatchEvent(new CustomEvent(name, { detail }));
};

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
 * Light haptic tap. No-op on the web preview.
 */
export async function tapHaptic(): Promise<void> {
  try {
    await Haptics.impact({ style: ImpactStyle.Light });
  } catch {
    // ignore on web preview
  }
}

/**
 * Persist a small key/value to the native secure store. Used by the
 * SPA's auth bridge for the refresh token on the native build.
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
