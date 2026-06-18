# Implementation Plan — Story 28.5 Mobile update notification

> Companion to [story-28-05-mobile-update-notification.md](story-28-05-mobile-update-notification.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Code home | `web/` SPA (mobile wraps `web/dist`); guarded by native detection. |
| Sources | paired server `GET /api/system/updates` first; GitHub Releases fallback. |
| Throttle/dismiss | `localStorage` (`mkt:update:lastCheck`, `mkt:update:dismissed`). |
| Banner | `web/src/components/UpdateBanner.tsx`; only renders on native. |
| Links | `web/src/lib/native.ts` (platform → store/apk URL). |

## 1. Shared logic — `web/src/lib/update.ts`

```ts
export interface UpdateInfo {
  available: boolean;
  current: string;
  latest?: string;
  releaseUrl?: string;
}

const DAY = 86_400_000;

export async function checkForUpdate(freq: "off"|"daily"|"weekly"): Promise<UpdateInfo|null> {
  if (freq === "off") return null;
  const window = freq === "weekly" ? 7*DAY : DAY;
  const last = Number(localStorage.getItem("mkt:update:lastCheck") || 0);
  if (Date.now() - last < window) return null;            // throttled
  localStorage.setItem("mkt:update:lastCheck", String(Date.now()));

  // 1) server source
  try {
    const s = await api.get<UpdateStatus>("/api/system/updates");
    return { available: s.available, current: s.current_version,
             latest: s.latest_version, releaseUrl: s.release_url };
  } catch { /* fall through */ }

  // 2) GitHub fallback (stable only on device)
  return githubFallback();
}

export function isDismissed(latest: string): boolean {
  return localStorage.getItem("mkt:update:dismissed") === latest; // per-version
}
export function dismiss(latest: string) {
  localStorage.setItem("mkt:update:dismissed", latest);
}
```

Reuses the same numeric `compareSemver` as the server (ported to TS, one
shared helper) so device and server agree on "newer".

## 2. Banner — `UpdateBanner.tsx`

```tsx
export function UpdateBanner() {
  const { t } = useI18n();
  const [info, setInfo] = useState<UpdateInfo|null>(null);
  useEffect(() => {
    if (!isNative()) return;                         // web/desktop use Settings UI
    checkForUpdate(readFreq()).then(i => {
      if (i?.available && i.latest && !isDismissed(i.latest)) setInfo(i);
    });
  }, []);
  if (!info?.latest) return null;
  return (
    <div className="mkt-update-banner" role="status">
      <span>{t("update.banner.available", { version: info.latest })}</span>
      <a href={storeOrApkUrl(info)} target="_blank" rel="noreferrer">{t("update.banner.get")}</a>
      <a href={info.releaseUrl} target="_blank" rel="noreferrer">{t("update.banner.notes")}</a>
      <button onClick={() => { dismiss(info.latest!); setInfo(null); }}>{t("common.dismiss")}</button>
    </div>
  );
}
```

Mounted once near the app shell (`App.tsx`).

## 3. Platform link resolution — `native.ts`

```ts
export function storeOrApkUrl(info: UpdateInfo): string {
  const p = platform();                  // 'ios' | 'android' | 'web'
  if (p === "ios") return IOS_APP_STORE_URL;
  if (p === "android") return isStoreBuild() ? PLAY_STORE_URL : apkUrl(info.latest!);
  return info.releaseUrl ?? RELEASES_PAGE_URL;
}
```

`isStoreBuild()` reads a build-time flag (`VITE_DISTRIBUTION=store|sideload`).

## 4. Test plan

| Test | Pins |
|---|---|
| `compareSemver` parity with Go | T01/T02 |
| `isDismissed` per-version | T03/T04 |
| throttle window respected | T05 |
| `storeOrApkUrl` per platform | T06/T07 |
| banner renders only on native + available + not dismissed | T08 |
| server-source preferred; GitHub fallback when unpaired | T08/T09 |

## 5. Acceptance checklist

- [ ] Shared `update.ts` (check/compare/throttle/dismiss).
- [ ] `UpdateBanner` native-only; dismiss-until-next-version.
- [ ] Server source first, GitHub fallback.
- [ ] Platform-correct store/apk links.
- [ ] i18n EN + AR; RTL-aware.
- [ ] Tests green.
