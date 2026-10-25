// Story 28.5 — the mobile app's own update. A store app cannot update itself (Apple and
// Google own that), so the Capacitor app (apps/mobile wraps this bundle) finds out whether a
// newer release exists and points at the right place to get it; it never downloads a binary.
//
//   - When: on launch, at most once per check frequency (off / daily / weekly; daily by
//     default). A check counts only when a source answered: offline it is a silent no-op,
//     tried again at the next launch. The release found last is kept, so the banner stays on
//     the later launches of the window until it is dismissed or the app is updated.
//   - Where from: the connected server's public GET /api/system/updates first (it follows the
//     server's channel, and no GitHub rate limit hits the phone), else GitHub Releases' latest
//     stable release (a phone never tracks beta by itself). A server whose checks are off says
//     so and is followed: no banner. GitHub's rate limit backs the fallback off until its reset.
//   - Dismissed: hidden for that version and every older one; a newer one shows again.
import { api } from "./api";
import { compareVersions, validVersion } from "./updatesApi";
import type { UpdateStatus } from "./updates";
import type { NativePlatform } from "./native";

export type CheckFrequency = "off" | "daily" | "weekly";

/** A release newer than the app, as the banner offers it. */
export interface AppRelease {
  /** "1.4.2" (no "v"). */
  version: string;
  /** The release's page (its notes). */
  releaseUrl: string;
  /** The sideload build's .apk on the release. */
  apkUrl: string;
  source: "server" | "github";
}

export type Distribution = "store" | "sideload";

const REPO = "Hamza-Labs-Core/Maktaba";
/** Android's store page (the app id of apps/mobile/capacitor.config.ts). */
export const PLAY_STORE_URL = "https://play.google.com/store/apps/details?id=com.hamzalabs.maktaba";

export const FREQUENCY_KEY = "mkt:update:frequency";
const LAST_CHECK_KEY = "mkt:update:lastCheck";
const LATEST_KEY = "mkt:update:latest";
const DISMISSED_KEY = "mkt:update:dismissed";
const BACKOFF_KEY = "mkt:update:backoffUntil";

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

type Store = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export interface CheckDeps {
  now: () => number;
  store: Store | null;
  /** The server's update status; rejects when no server answers. */
  serverStatus: () => Promise<UpdateStatus | null | undefined>;
  fetchFn: typeof fetch;
}

function safeLocal(): Store | null {
  try {
    return window.localStorage;
  } catch {
    return null; // storage refused (a private window)
  }
}

const defaults = (): CheckDeps => ({
  now: Date.now,
  store: safeLocal(),
  serverStatus: () => api.get<UpdateStatus>("/api/system/updates"),
  fetchFn: (...args) => fetch(...args),
});

function get(store: Store | null, key: string): string | null {
  try {
    return store?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

function set(store: Store | null, key: string, value: string | null): void {
  try {
    if (value === null) store?.removeItem(key);
    else store?.setItem(key, value);
  } catch {
    // storage full or refused: the check simply runs again next launch
  }
}

const bare = (v: string): string => v.trim().replace(/^v/i, "");

/** The check frequency the user chose (daily when never chosen). */
export function readFrequency(store: Store | null = safeLocal()): CheckFrequency {
  const f = get(store, FREQUENCY_KEY);
  return f === "off" || f === "weekly" ? f : "daily";
}

export function writeFrequency(f: CheckFrequency, store: Store | null = safeLocal()): void {
  set(store, FREQUENCY_KEY, f);
}

/** Whether a check is due now: checks on, the frequency's window passed since the last one
 * that answered, and no rate-limit backoff running. */
export function checkDue(freq: CheckFrequency, now: number, store: Store | null): boolean {
  if (freq === "off") return false;
  if (now < Number(get(store, BACKOFF_KEY) ?? 0)) return false;
  const window = freq === "weekly" ? 7 * DAY : DAY;
  return now - Number(get(store, LAST_CHECK_KEY) ?? 0) >= window;
}

/** Whether the banner for `version` was dismissed (that version, or a newer one). */
export function isDismissed(version: string, store: Store | null = safeLocal()): boolean {
  const d = get(store, DISMISSED_KEY);
  return d !== null && validVersion(d) && compareVersions(version, d) <= 0;
}

export function dismissVersion(version: string, store: Store | null = safeLocal()): void {
  set(store, DISMISSED_KEY, bare(version));
}

function releaseUrlFor(version: string): string {
  return `https://github.com/${REPO}/releases/tag/v${version}`;
}

/** The sideload build's .apk on the release (mobile-release.yml: maktaba-<version>.apk). */
function apkUrlFor(version: string): string {
  return `https://github.com/${REPO}/releases/download/v${version}/maktaba-${version}.apk`;
}

const apkName = (version: string) => `maktaba-${version}.apk`;

function httpsUrl(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  try {
    return new URL(raw).protocol === "https:" ? raw : null;
  } catch {
    return null;
  }
}

/** GitHub said too many requests: back off until `until` (ms). */
class RateLimited extends Error {
  constructor(readonly until: number) {
    super("GitHub rate limit");
  }
}

interface GitHubRelease {
  tag_name?: string;
  html_url?: string;
  draft?: boolean;
  prerelease?: boolean;
  assets?: { name?: string; browser_download_url?: string }[];
}

async function fromGitHub(d: CheckDeps): Promise<AppRelease | null> {
  const res = await d.fetchFn(`https://api.github.com/repos/${REPO}/releases/latest`, {
    headers: { Accept: "application/vnd.github+json" },
  });
  if (res.status === 403 || res.status === 429) {
    const reset = Number(res.headers.get("x-ratelimit-reset") ?? 0) * 1000;
    const retry = Number(res.headers.get("retry-after") ?? 0) * 1000;
    throw new RateLimited(reset > d.now() ? reset : d.now() + (retry > 0 ? retry : HOUR));
  }
  if (res.status === 404) return null; // no release published yet
  if (!res.ok) throw new Error(`GitHub ${res.status}`);
  const rel = (await res.json()) as GitHubRelease;
  const version = bare(rel.tag_name ?? "");
  // /releases/latest is the newest stable release already; a beta never reaches a phone here
  if (rel.draft || rel.prerelease || !validVersion(version) || version.includes("-")) return null;
  const apk = rel.assets?.find((a) => a.name === apkName(version));
  return {
    version,
    releaseUrl: httpsUrl(rel.html_url) ?? releaseUrlFor(version),
    apkUrl: httpsUrl(apk?.browser_download_url) ?? apkUrlFor(version),
    source: "github",
  };
}

function fromServer(s: UpdateStatus & { assets?: { name?: string; url?: string }[] }): AppRelease {
  const version = bare(s.latest_version ?? "");
  const apk = s.assets?.find((a) => a.name === apkName(version));
  return {
    version,
    releaseUrl: httpsUrl(s.release_url) ?? releaseUrlFor(version),
    apkUrl: httpsUrl(apk?.url) ?? apkUrlFor(version),
    source: "server",
  };
}

/**
 * The latest release for this phone: the server's word when it answers (null when its checks
 * are off), else GitHub's latest stable release (a server with no latest yet, or none at all).
 * Rejects when neither answers.
 */
export async function latestRelease(d: CheckDeps): Promise<AppRelease | null> {
  let s: UpdateStatus | null | undefined;
  try {
    s = await d.serverStatus();
  } catch {
    s = undefined; // no server answers: GitHub
  }
  if (s?.disabled) return null; // the owner turned this server's update checks off
  if (s?.latest_version && validVersion(s.latest_version)) return fromServer(s);
  return fromGitHub(d);
}

function readLatest(store: Store | null): AppRelease | null {
  try {
    const r = JSON.parse(get(store, LATEST_KEY) ?? "null") as AppRelease | null;
    return r && typeof r.version === "string" && validVersion(r.version) ? r : null;
  } catch {
    return null;
  }
}

/**
 * What the banner offers on this launch for an app at `current`: the release newer than it
 * (checked now when a check is due, else the one found last), or null — checks off, the app
 * up to date (or ahead, a test build), the release dismissed, or nothing known.
 */
export async function appUpdateToOffer(
  current: string,
  deps: Partial<CheckDeps> = {}
): Promise<AppRelease | null> {
  const d = { ...defaults(), ...deps };
  const freq = readFrequency(d.store);
  if (freq === "off") return null;
  let latest = readLatest(d.store);
  if (checkDue(freq, d.now(), d.store)) {
    try {
      latest = await latestRelease(d);
      set(d.store, LAST_CHECK_KEY, String(d.now()));
      set(d.store, LATEST_KEY, latest ? JSON.stringify(latest) : null);
    } catch (e) {
      if (e instanceof RateLimited) set(d.store, BACKOFF_KEY, String(e.until));
      // offline, or GitHub failed: what was found last stands; tried again next launch
    }
  }
  if (!latest || compareVersions(latest.version, current) <= 0) return null;
  if (isDismissed(latest.version, d.store)) return null;
  return latest;
}

/** The build's distribution (Android): the Play Store build or the sideloaded .apk. */
export function distribution(): Distribution {
  return import.meta.env?.VITE_DISTRIBUTION === "store" ? "store" : "sideload";
}

/** The App Store page (iOS), when the build knows it. */
export function iosAppStoreUrl(): string | null {
  return httpsUrl(import.meta.env?.VITE_IOS_APP_STORE_URL);
}

/**
 * Where the banner's action goes: the App Store on iOS (the release page while the build
 * knows no App Store page), the Play Store for Android's store build, the .apk for its
 * sideload build, the release page anywhere else.
 */
export function actionUrl(
  rel: AppRelease,
  platform: NativePlatform,
  dist: Distribution = distribution(),
  appStore: string | null = iosAppStoreUrl()
): string {
  if (platform === "ios") return appStore ?? rel.releaseUrl;
  if (platform === "android") return dist === "store" ? PLAY_STORE_URL : rel.apkUrl;
  return rel.releaseUrl;
}
