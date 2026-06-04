/// <reference types="node" />
import type { CapacitorConfig } from '@capacitor/cli';

// Maktaba mobile (Capacitor) — wraps the shared web/ SPA bundle.
//
// The web bundle is produced by `pnpm --filter maktaba-web build` into
// web/dist; `webDir` points the native shells at that output so iOS and
// Android serve the exact same JS the desktop/web clients run.
//
// DEV LIVE-RELOAD
// ---------------
// In production the native app loads the bundled `webDir`. During
// development you can instead point the WebView at a running Vite dev
// server so edits hot-reload on the device/simulator without a re-sync:
//
//   # phone on the same Wi-Fi → use the host machine's LAN IP, not
//   # localhost (localhost on a device resolves to the device itself).
//   CAP_SERVER_URL=http://192.168.1.20:5173 npx cap run ios
//
// The Vite dev server (web/vite.config.ts) already proxies `/api` and
// `/ws` to the local Go API on :8080, so a single URL gives the WebView
// both the SPA and the API. `cleartext` is enabled only for this
// http://-on-LAN dev case; production stays https/custom-scheme only.
const devServerUrl = process.env.CAP_SERVER_URL?.trim();

const config: CapacitorConfig = {
  appId: 'com.hamzalabs.maktaba',
  appName: 'Maktaba',
  webDir: '../../web/dist',
  // Set false explicitly: the Capacitor 6 runtime is provided by the
  // native bridge, never bundled into the web JS (keeps web/ free of
  // @capacitor/* — see web/src/lib/native.ts).
  bundledWebRuntime: false,

  server: devServerUrl
    ? {
        // Dev live-reload: load from the Vite dev server over the LAN.
        url: devServerUrl,
        cleartext: devServerUrl.startsWith('http://'),
      }
    : {
        // Production: serve the bundled webDir. https on Android, a
        // custom `maktaba` scheme on iOS so Service Workers + secure
        // contexts behave, and no cleartext.
        androidScheme: 'https',
        iosScheme: 'maktaba',
        cleartext: false,
      },

  ios: {
    contentInset: 'automatic',
    scrollEnabled: true,
    preferredContentMode: 'mobile',
    // Required for iOS Universal Links / App-Bound domains (Story 12.9).
    limitsNavigationsToAppBoundDomains: true,
  },

  android: {
    allowMixedContent: false,
    captureInput: true,
    webContentsDebuggingEnabled: false,
  },

  plugins: {
    // Splash screen (Story 12.x). Hidden after first paint by the
    // native bridge (native-shell.ts) to avoid a white flash.
    SplashScreen: {
      launchShowDuration: 1000,
      launchAutoHide: false, // the bridge calls SplashScreen.hide()
      backgroundColor: '#1E5AD8',
      androidSplashResourceName: 'splash',
      androidScaleType: 'CENTER_CROP',
      showSpinner: false,
    },

    // Status bar — tint reconciled to the SPA's data-theme by the
    // bridge (light/dark) at runtime.
    StatusBar: {
      style: 'DEFAULT',
      backgroundColor: '#1E5AD8',
      overlaysWebView: false,
    },

    // Push notifications — FCM (Android) / APNs (iOS). The device row is
    // registered server-side via POST /api/devices (bundle_id =
    // com.hamzalabs.maktaba). Credentials (google-services.json,
    // APNs key + entitlements) are provisioned per-platform outside VCS.
    PushNotifications: {
      presentationOptions: ['badge', 'sound', 'alert'],
    },

    // Secure storage — Keychain (iOS) / EncryptedSharedPreferences via
    // the Android Keystore. Backs the auth refresh-token persistence on
    // the native build (see nativeSecureStore in native-shell.ts).
    SecureStoragePlugin: {
      // @aparajita/capacitor-secure-storage reads no static config; the
      // key prefix / access group are set at call sites. Present here so
      // the plugin block documents the security surface in one place.
    },
  },
};

export default config;
