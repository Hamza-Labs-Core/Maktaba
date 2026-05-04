# Implementation Plan — Story 12.9 Deep Linking

> Companion to [story-12-09-deep-linking.md](story-12-09-deep-linking.md).
> Universal/App Links + custom scheme `maktaba://`.
> Server publishes `apple-app-site-association` and `assetlinks.json` from `/.well-known/`.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Plugin | `@capacitor/app` for `appUrlOpen` events; native side handles cold launch arguments. |
| iOS | Universal Links via `apple-app-site-association` + Associated Domains entitlement. |
| Android | App Links via `assetlinks.json` + intent filter `android:autoVerify="true"`. |
| Custom scheme | `maktaba://` registered on both platforms. |
| Routes | `/watch/{id}?t=`, `/search?q=`, `/library`, `/library/{id}`, `/queue`, `/settings`, `/collection/{id}`. |
| Auth replay | If unauthenticated, store deep link, redirect to login, replay after success. |
| Out of scope | Server-side `.well-known` (small; included here as a one-line owner statement). |

## 1. Server `.well-known` files

Owned here (single-file additions to API service):

```jsonc
// /.well-known/apple-app-site-association
{ "applinks": { "details": [
  { "appIDs": ["TEAMID.com.maktaba.app"], "components": [
    { "/": "/watch/*" }, { "/": "/library*" }, { "/": "/search*" },
    { "/": "/queue" }, { "/": "/settings" }, { "/": "/collection/*" }
  ]}
]}}

// /.well-known/assetlinks.json
[{
  "relation": ["delegate_permission/common.handle_all_urls"],
  "target": { "namespace": "android_app", "package_name": "com.maktaba.app",
              "sha256_cert_fingerprints": ["..."] }
}]
```

API service serves these as static, MIME `application/json`, no `Content-Encoding` (Apple requires raw JSON).

## 2. iOS configuration

`Info.plist`:

```xml
<key>CFBundleURLTypes</key><array><dict>
  <key>CFBundleURLName</key><string>com.maktaba.app</string>
  <key>CFBundleURLSchemes</key><array><string>maktaba</string></array>
</dict></array>
```

`App.entitlements`:

```xml
<key>com.apple.developer.associated-domains</key>
<array><string>applinks:{server_host}</string></array>
```

Cold launch handling in `AppDelegate.swift`:

```swift
func application(_ app: UIApplication, open url: URL, options: [UIApplication.OpenURLOptionsKey : Any] = [:]) -> Bool {
    NotificationCenter.default.post(name: .maktabaDeepLink, object: url)
    return true
}
func application(_ app: UIApplication, continue userActivity: NSUserActivity, restorationHandler: @escaping ([UIUserActivityRestoring]?) -> Void) -> Bool {
    if let url = userActivity.webpageURL {
        NotificationCenter.default.post(name: .maktabaDeepLink, object: url)
    }
    return true
}
```

## 3. Android configuration

`AndroidManifest.xml` adds intent filters to `MainActivity`:

```xml
<intent-filter android:autoVerify="true">
  <action android:name="android.intent.action.VIEW"/>
  <category android:name="android.intent.category.DEFAULT"/>
  <category android:name="android.intent.category.BROWSABLE"/>
  <data android:scheme="https" android:host="${SERVER_HOST}"/>
</intent-filter>
<intent-filter>
  <action android:name="android.intent.action.VIEW"/>
  <category android:name="android.intent.category.DEFAULT"/>
  <category android:name="android.intent.category.BROWSABLE"/>
  <data android:scheme="maktaba"/>
</intent-filter>
```

`MainActivity.onNewIntent` and `onCreate` route the URL via the Capacitor bridge.

## 4. Web router glue

```ts
// web/src/features/deeplink/notificationRouter.ts
import { App } from '@capacitor/app';

export function installDeepLinkRouter(navigate: NavigateFunction, auth: AuthCtx) {
  App.addListener('appUrlOpen', ({ url }) => {
    const target = parseTarget(url);
    if (!target) { navigate('/library'); return; }
    if (!auth.isAuthenticated()) {
      sessionStorage.setItem('maktaba.postLoginRedirect', target.path);
      navigate('/login');
      return;
    }
    navigate(target.path);
  });
}

function parseTarget(url: string): { path: string } | null {
  const u = new URL(url);
  // accept https://{host}/... and maktaba://...
  const path = u.pathname || '/' + (u.host + u.pathname);
  if (/^\/watch\/[A-Za-z0-9_-]+/.test(path)) return { path: path + (u.search || '') };
  if (/^\/search/.test(path)) return { path: path + (u.search || '') };
  if (path === '/library' || /^\/library\/[A-Za-z0-9_-]+/.test(path)) return { path };
  if (path === '/queue' || path === '/settings') return { path };
  if (/^\/collection\/[A-Za-z0-9_-]+/.test(path)) return { path };
  return null;
}
```

After login, the auth redirect handler reads `sessionStorage.maktaba.postLoginRedirect` and navigates.

## 5. Edge cases

| Case | Handling |
|---|---|
| Deleted resource (404) | Inline "Video not found" + "Return to library" CTA. |
| Server URL changed (different host) | Surface "This link points to a different Maktaba server" + CTA to switch configured server. |
| Malformed deep link | Log warning, navigate to `/library`. |
| Notification deep link with `maktaba://job/123` | Routes to `/queue?focus=123`. |

## 6. Test cases

### 6.1 Unit

| Test | Asserts |
|---|---|
| `parses /watch/abc?t=120` | `target.path === '/watch/abc?t=120'`. |
| `unauth saves redirect` | `sessionStorage` set; navigate `/login`. |
| `malformed url falls back to /library` | Returns `null`; navigate `/library`. |
| `maktaba://search?q=x` parses` | Path `/search?q=x`. |

### 6.2 Manual

- Tap `https://{server}/watch/abc?t=120` from Mail with app installed → opens at 02:00.
- Tap same URL without app → web fallback in Safari/Chrome.
- Tap notification with `maktaba://job/123` → Queue tab focused.

## 7. Performance

- Deep link handling latency ≤ 100 ms from `appUrlOpen` event to `navigate`.

## 8. Dependencies

- Story 12.4 (notification payload includes deep link).
- Story 12.10 builds the device-aware notification payload.
- Auth flow (Epic 10).
