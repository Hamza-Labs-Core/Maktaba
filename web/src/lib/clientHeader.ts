// X-Maktaba-Client (Epic 44 story 44.2): every request says which app sent it,
// `<app>/<version> (<platform>; <device>)`. The same web bundle runs as three
// apps: `web` in a browser, `desktop` in the Tauri shell, `mobile` in the
// Capacitor shell. The server reads it as a label only (analytics, the API's
// use per app), never for rights; these three still sign in with the cookie.
import { isDesktop } from "./desktop";
import { isNativeRuntime } from "./native";

export const CLIENT_HEADER = "X-Maktaba-Client";

export interface ClientParts {
  app: "web" | "desktop" | "mobile";
  version: string;
  platform: string;
  device: string;
}

// clientPlatform maps a user agent to the platform names the server knows.
export function clientPlatform(ua: string): string {
  const s = ua.toLowerCase();
  if (/ipad/.test(s)) return "ipados";
  if (/iphone|ipod/.test(s)) return "ios";
  if (/android/.test(s)) return "android";
  if (/mac os|macintosh/.test(s)) return "macos";
  if (/windows/.test(s)) return "windows";
  if (/linux/.test(s)) return "linux";
  return "web";
}

// clientDevice is the kind of device: a tablet or a phone by the user agent,
// else a desktop (the desktop shell always is one).
export function clientDevice(ua: string, app: ClientParts["app"]): string {
  if (app === "desktop") return "desktop";
  const s = ua.toLowerCase();
  if (/ipad|tablet/.test(s) || (/android/.test(s) && !/mobile/.test(s))) return "tablet";
  if (/iphone|ipod|android|mobile/.test(s)) return "phone";
  return "desktop";
}

export function clientParts(ua: string, version: string): ClientParts {
  let app: ClientParts["app"] = "web";
  if (isDesktop()) app = "desktop";
  else if (isNativeRuntime()) app = "mobile";
  return {
    app,
    version: version || "dev",
    platform: clientPlatform(ua),
    device: clientDevice(ua, app),
  };
}

export function formatClient(p: ClientParts): string {
  return `${p.app}/${p.version} (${p.platform}; ${p.device})`;
}

let cached: string | null = null;

/** The X-Maktaba-Client value of this app (worked out once). */
export function clientHeaderValue(): string {
  if (cached === null) {
    const ua = typeof navigator !== "undefined" ? navigator.userAgent : "";
    const version = (import.meta.env?.VITE_APP_VERSION as string | undefined) ?? "dev";
    cached = formatClient(clientParts(ua, version));
  }
  return cached;
}

/** Tests: forget the worked-out value. */
export function resetClientHeaderForTest(): void {
  cached = null;
}
