# Story 50.13 — Apps ride out a cloud outage

> Epic 50 · Phase 1 · Depends on: 50.11 · Needed by: launch gate (README §11)

## Why

The cloud's job is to make a home reachable, so its outage should cost as little as possible.
Today the apps ask `api.maktaba.tv` for the home list (`GET /v1/homes`) every time and don't
keep it: Apple `Cloud.swift` builds its session with "no cookies … no cache", Android
`Cloud.kt` `homes()` calls the API directly, and the cloud web app's `Homes.tsx` reads it live.
So when the API is down, an app that could reach the home directly can't even find its
address. And when the relay answers `502 server_offline` during a reconnect, the apps show a
generic error.

Plex is the warning ([research.md](research.md) §2): its July 2026 outage stopped people from
reaching even their local servers. Maktaba's home already signs its own users in; this story
makes the apps use that.

## Description

### 1. A cached connection list

After every successful `GET /v1/homes` the app saves, per cloud account, on disk:

```json
{ "saved_at": "2026-11-03T10:15:00Z",
  "homes": [ { "server_id": "...", "name": "...", "slug": "...",
               "connections": [
                 { "url": "https://192-168-1-20.<home-id>.direct.maktaba.tv:8443", "kind": "local" },
                 { "url": "https://81-2-3-4.<home-id>.direct.maktaba.tv:32443",  "kind": "direct" },
                 { "url": "https://<slug>.r3.relay.maktaba.tv",                  "kind": "relay_hint" },
                 { "url": "https://<slug>.relay.maktaba.tv",                     "kind": "relay" } ] } ] }
```

- The API grows a `connections` array (keeping `direct_url` and `relay_url` for older apps).
  Until 50.15 and 50.16 ship, it holds only what exists today: `direct_url` (plain HTTP) and
  `relay_url`.
- Stored with the platform's normal app storage: Apple `UserDefaults` (beside the saved homes
  in `Homes.swift`), Android the `TokenStore` key-value store, web `localStorage` in the cloud
  web app. No tokens in it.
- Used when the API can't be reached (DNS failure, connect timeout, 5xx). Kept **30 days**;
  older is still shown but marked as old.
- Deleted on sign-out.

### 2. The order apps try

1. **Local** (mDNS-found or LAN addresses, as today's `HomeDiscovery`).
2. **Direct** (public address; HTTPS once 50.15 ships).
3. **Relay hint** (50.16).
4. **Relay** (the stable `<slug>.relay.maktaba.tv`).

The app races 1 and 2 for 1 s (story 25.10), then adds 3 and 4. The winner is pinned for 5
minutes or until a network change, as today.

### 3. Signing in while the cloud is down

- A home session the app already has keeps working; the app never asks the cloud to renew it.
- A **new** sign-in with a Maktaba Cloud account needs the cloud's assertion
  (`POST /v1/homes/{id}/assertion`). If the cloud doesn't answer, the app says so and offers the
  home's own sign-in (username and password, or a code), which works without the cloud.

### 4. A home that is reconnecting

The relay answers, after 50.04's 15-second hold, `503 home_reconnecting` with `Retry-After`.
Players and lists treat it like a short blip: retry with backoff (2 s, 4 s, 8 s, cap 15 s, up to
60 s in all), show "Reconnecting to your home…" after the first retry, and only then the
offline message. `502 server_offline` (home not connected at all) keeps today's
home-offline screen.

### 5. The status banner

Every app shows 50.11's banner when the API or relay component is not operational, so a user
looking at the "can't reach Maktaba Cloud" message also sees it is a known outage.

## Acceptance criteria

- **Given** an app that listed its homes once,
  **when** `api.maktaba.tv` is unreachable and the app starts,
  **then** it shows the saved homes with "Showing your homes from {time}" and connects to a home
  through a saved connection that answers.
- **Given** the app is on the home's LAN and the cloud is unreachable,
  **when** the user plays a video with an existing home session,
  **then** it plays, and no request goes to `maktaba.tv`.
- **Given** the cloud is unreachable,
  **when** the user tries to sign in to a home with a Maktaba Cloud account,
  **then** the app explains the cloud can't be reached and offers the home's own sign-in.
- **Given** a relayed stream,
  **when** the relay answers `503 home_reconnecting` for 20 s and then serves again,
  **then** playback resumes on its own, and the user sees "Reconnecting to your home…" at most.
- **Given** a saved list older than 30 days,
  **when** the API is unreachable,
  **then** the homes still show, marked as old.
- **Given** the user signs out of Maktaba Cloud,
  **when** sign-out finishes,
  **then** the saved list is deleted.
- **Given** an app version from before this story,
  **when** it reads `/v1/homes`,
  **then** `direct_url` and `relay_url` are still there and it works as before.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (Apple `CloudTests`) | homes fetched, then API fails | `homes()` | saved list returned with `savedAt` |
| T02 | unit (Android `CloudTest`) | same | `homes()` | same |
| T03 | unit (cloud web `homes.test.tsx`) | same | render Homes | saved homes + "from {time}" note |
| T04 | unit (all) | connections of every kind | order | local, direct, relay hint, relay |
| T05 | unit (all) | `503 home_reconnecting` × 3 then 200 | load a page | retried; reconnecting text shown; success |
| T06 | unit (all) | assertion call times out | cloud sign-in | message + home sign-in offered |
| T07 | unit (cloud) | `GET /v1/homes` | response | has `connections`, `direct_url`, `relay_url` |
| T08 | integration (CI) | home stack + an app client library (Android `client`, MaktabaKit), cloud host blackholed | sign in with home password, start playback | plays; zero requests to the cloud host |
| T09 | rehearsal (CI, 50.06 `apps-cloud-down`) | rehearsal pair stopped, home running | client library run as T08 | as T08 |
| T10 | Arabic | new keys | strings check | Arabic present in web, cloud web, Android `values-ar`, Apple catalog |
| T11 | UI (CI only) | Player shards, mocked cloud down | launch | saved homes visible, banner shown |

## Edge cases

- **The saved address is stale.** A changed public IP fails the direct try; the relay entry is
  still there. The next successful API call refreshes the list.
- **Captive portals.** A hotel portal answering every request with 200 must not look like a
  live API: the app checks the response is JSON from the API (as today's normalisation does).
- **Two cloud accounts on one device.** One saved list per account.
- **Security.** The list holds addresses and names only. Addresses are as sensitive as the
  home list itself, which the app already shows; nothing is written outside the app's own
  storage.
- **Web.** The home's own web app reached through the relay can't help when the relay is down;
  the cloud web app shows saved direct links instead.

## User-facing strings

| Key | EN | AR |
|---|---|---|
| `cloud.homes.saved` | Maktaba Cloud can't be reached. Showing your homes from {time}. | تعذّر الوصول إلى سحابة مكتبة. نعرض أجهزتك الرئيسية كما كانت في {time}. |
| `cloud.homes.savedOld` | This list is more than 30 days old. Some addresses may have changed. | عمر هذه القائمة أكثر من 30 يومًا، وقد تكون بعض العناوين تغيّرت. |
| `cloud.signIn.cloudDown` | Signing in with Maktaba Cloud isn't working right now. Sign in to your home with its username and password, or with a code. | تسجيل الدخول عبر سحابة مكتبة لا يعمل الآن. سجّل الدخول إلى الجهاز الرئيسي باسم المستخدم وكلمة المرور، أو برمز. |
| `cloud.signIn.useHome` | Sign in with the home's password | تسجيل الدخول بكلمة مرور الجهاز الرئيسي |
| `home.reconnecting` | Reconnecting to your home… | جارٍ إعادة الاتصال بالجهاز الرئيسي… |
| `home.reconnectFailed` | Your home didn't come back in time. It may be restarting. Try again in a moment. | لم يعد الجهاز الرئيسي في الوقت المحدد، وربما يُعاد تشغيله. حاول مجددًا بعد قليل. |

## Build plan

### Files to touch

| File | Change |
|---|---|
| `cloud/internal/handlers/homes/homes.go` | `connections` array in `homeView`; keep `direct_url`, `relay_url`. |
| `cloud/internal/handlers/relay/proxy.go` | `503 home_reconnecting` + `Retry-After` after the hold (with 50.04). |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/Cloud.swift`, `Homes.swift` | Save and read the list; order connections; cloud-down sign-in path. |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/APIClient.swift`, `HomeErrors.swift` | `home_reconnecting` retry and wording. |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/CloudViews.swift`, `Localizable.xcstrings` | Saved-list note; strings. |
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/session/Cloud.kt`, `TokenStore.kt`, `Connect.kt`, `AppSession.kt` | Save and read; order; retry; cloud-down sign-in. |
| `apps/android/core/src/main/res/values/strings.xml`, `values-ar/strings.xml` (and player/remote where the screens live) | Strings. |
| `web/cloud/src/pages/Homes.tsx`, `web/cloud/src/api.ts`, `web/cloud/src/i18n/en.json`, `ar.json` | Saved list in `localStorage`; note; direct links. |
| `web/src/` API client and `web/src/i18n/en.json`, `ar.json` | `home_reconnecting` retry and text in the home's web app. |

### Tests

- T01–T07, T10 in each platform's unit jobs; T08 as an integration test in the Android
  `client` module and MaktabaKit tests with the cloud host pointed at a closed port.
- UI test T11 only in CI shards, on a throwaway simulator.
- **CI only:** rehearsal scenario `apps-cloud-down` (50.06).

### Rollout

1. Cloud: add `connections` (additive) and `home_reconnecting`.
2. Apps: ship in the next release on every platform together (Arabic in the same PR).
3. No server-side switch needed; old apps keep reading the old fields.

### Rollback

- Apps: the saved list is only read when the API fails, so a bad build is fixed by the next
  release; nothing on the cloud depends on it.
- Cloud: `home_reconnecting` falls back to `502 server_offline` with
  `relay.hold.enabled = false` (50.04).

### Dependencies

50.11 (the banner). Uses 50.04's hold, 50.15's HTTPS direct names and 50.16's hints when they
exist; works without them.
