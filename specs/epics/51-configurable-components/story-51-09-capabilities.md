# Story 51.09 — Capabilities and the `component_off` answer

> Epic 51 · Phase 2 · Depends on: 51.08 · Needed by: 51.11

## Why

No client can tell today that a part of the home is off (research §1.7). `GET /api/me/flags`
carries licence flags only, and `useFlag` is used by one settings section. So a home with Live
TV off still shows a Live TV tab, and the tab fails with a generic error. This story gives every
app one small answer it can read and keep fresh, a push when it changes, and one error code with
words in English and Arabic for a route whose component is off. Old apps keep working.

## Description

### 1. Features in the catalog

51.01 gives each component a `features` list (the user-facing words it serves). This story
reads it and fixes the rule that turns it into one answer per feature:

- a feature is **on** when every component that lists it is on;
- except members of a `conflicts` pair that both list it (`livetv.slots`, `livetv.inprocess`):
  one of the pair on is enough;
- a feature no component lists is always on.

| Feature | Listed by (51.01's catalog) |
|---|---|
| `live_tv` | `livetv`, and one of `livetv.slots` / `livetv.inprocess` |
| `transcripts` | `transcription` |
| `subtitles` | `subtitles` |
| `semantic_search` | `search.semantic` |
| `chapters` | none today (51.01 leaves `media.chapters` out until it has a stage): always on |
| `youtube` | `youtube` |
| `plex` | `ext.plex` |
| `remote_access` | `cloud.relay` |
| `recommendations` | none (see Edge cases): always on |

The `catalog` CI job (51.01) also checks: every feature word is one of the fixed nine, and each
has `components.feature.<id>` words in English and Arabic. Words are never renamed. A
retired feature stays in the answer as `"off"` for one year.

### 2. `GET /api/capabilities`

Any signed-in user, including viewers and cloud-linked play sign-ins. No sign-in is `401`.

```json
{ "etag": "c-1842-7",
  "features": { "live_tv": "on", "transcripts": "on", "subtitles": "on",
                "semantic_search": "off", "chapters": "on", "youtube": "off",
                "plex": "on", "remote_access": "on", "recommendations": "on" },
  "admin": { "manage_components": true,
             "off": { "semantic_search": { "components": ["search.semantic"],
                                           "reason": "switched_off" } } } }
```

- **A feature is `on`** when every component it needs has `want = enabled`, no hold, and the
  `Licensed` and `Installed` conditions true. It reads desired state and those two
  conditions only. A crash or a restart does not flip it: a tab never disappears because a
  process restarted, the screen shows the error it got instead.
- **`admin`** is present only for admins (and `manage` cloud links). It names the components
  and the reason code for each feature that is off, so the admin's app can explain it. Viewers
  get no component ids, machines, versions or reasons (H12).
- **ETag** is `c-<max desired generation>-<licence revision>`. `If-None-Match` answers `304`.
  The value is cached in memory and dropped on `components_changed` and on the licence-change
  event, so the answer costs no query in the common case: **≤ 20 ms p95, ≤ 1 KB** (N11).
- `Cache-Control: private, no-cache`.

### 3. The push

The apps' live channel is the SSE fan-out under `/ws/*` (`api/internal/handlers/ws/ws.go`),
which has channels for jobs, queues, a library and a video, but none every user listens to.
This story adds one: **`/ws/home`**, open to any signed-in user, carrying home-wide events.
Its first event:

```json
{ "type": "capabilities_changed", "at": "…", "payload": { "etag": "c-1843-7" } }
```

It goes through the cross-replica event bus (`ws/eventbus`), so every API replica and every
reconnecting client gets it (replay by `last_event_id`, as today). The payload holds only the
ETag. Clients refetch `GET /api/capabilities`, so a viewer never sees the admin block through
the push. The API publishes it after a desired-state change or a licence change, at most once a
second (changes are coalesced).

Clients fetch at sign-in, when the app comes to the front, every **5 minutes** with the ETag,
and on `capabilities_changed`. A failed fetch keeps the last good answer. With no answer yet,
every feature counts as on, which is today's behaviour.

### 4. `409 component_off`

A middleware in the router maps each route to the component it needs, in one table beside the
placement table (`api/internal/router/placement.go`):

```go
var ComponentRoutes = componentTable{
  {Exact: "GET /api/channels/now", Component: "livetv"},
  {Exact: "GET /api/channels/guide", Component: "livetv"},
  {Exact: "POST /api/tv/remote/tune", Component: "livetv"}, …
}
```

- Only routes that need the **running** part are mapped: the guide and what is on now,
  tuning a channel, a YouTube fetch, a Plex sign-in. The full list is written while building,
  route by route. Settings and configuration routes stay
  core, so an admin can prepare a feature before turning it on.
- When the component's desired state is not enabled-and-unheld, the answer is:

```json
{ "type": "https://maktaba.tv/problems/component-off", "title": "This feature is off",
  "status": 409, "code": "component_off", "component": "livetv", "feature": "live_tv" }
```

- The check reads the same in-memory snapshot as capabilities: no query per request.
- `TestEveryRouteHasComponent` fails when a newly mounted route is neither in the table nor
  marked core, like `TestEveryRouteHasPlacementClass`.
- Search does not answer `409` when semantic search is off. It skips the semantic leg at once
  and answers keyword results with `"semantic": "off"`. Today it waits for the gRPC deadline
  and answers `degraded: true` (`api/internal/handlers/search/search.go:199`).

### 5. Words in every client

A new error kind **`off`** joins the shared kinds (`notFound`, `busy`, …) on every platform:
`status == 409 && code == "component_off"`. Its words:

| Key (web / Android / Apple) | English | Arabic |
|---|---|---|
| `error.kind.off` / `error_off` / `.off` | This feature is turned off on this home. | هذه الميزة متوقفة على الجهاز الرئيسي. |
| `error.kind.offAdmin` / `error_off_admin` / `.offAdmin` | %@ is off on this home. Turn it on in Components. | %@ متوقفة على الجهاز الرئيسي. شغّلها من صفحة المكوّنات. |
| `components.feature.live_tv` (and the other 8) | Live TV | البث المباشر |

Each feature has a name in both languages (`transcripts` نصوص الفيديو, `subtitles` الترجمة,
`semantic_search` البحث بالمعنى, `chapters` الفصول, `youtube` يوتيوب, `plex` Plex,
`remote_access` الوصول من خارج المنزل, `recommendations` الاقتراحات). The existing parity
tests (web `en.json`/`ar.json`, Android `values`/`values-ar`, Apple String Catalog "has its
Arabic" step) check them. 51.11 uses these words on screens; this story adds the kind and the
words so a `409` already reads well.

## Acceptance criteria

- **Given** `search.semantic` is disabled,
  **when** a viewer calls `GET /api/capabilities`,
  **then** `semantic_search` is `"off"` and the answer has no `admin` block.
- **Given** the same,
  **when** an admin calls it,
  **then** `admin.off.semantic_search` names `search.semantic` and `switched_off`.
- **Given** a client holds ETag `c-1842-7`,
  **when** nothing changed and it sends `If-None-Match`,
  **then** the answer is `304` with no body.
- **Given** a viewer listening on `/ws/home`,
  **when** an admin disables `youtube`,
  **then** the viewer gets `capabilities_changed` within 2 s with the new ETag, and the next
  fetch shows `youtube: "off"`.
- **Given** `livetv` is off,
  **when** any app requests `GET /api/channels/now`,
  **then** the answer is `409` with `code = component_off`, `component = livetv`,
  `feature = live_tv`.
- **Given** `livetv` is off,
  **when** an admin opens the channel settings,
  **then** they load and save as before.
- **Given** semantic search is off,
  **when** a user searches,
  **then** keyword results come back with no wait and no error.
- **Given** the benchmark,
  **when** `GET /api/capabilities` runs 10,000 times,
  **then** p95 ≤ 20 ms and the body ≤ 1 KB.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit | catalog fixture | resolve with `livetv` on, both slot kinds off; then `livetv.inprocess` on | `live_tv` off; then on |
| T02 | unit | `transcription` enabled, held | resolve | `transcripts` off |
| T03 | unit | `transcription` enabled, Failed (crash loop) | resolve | `transcripts` on |
| T04 | role | capabilities | nil, viewer, play sign-in, admin | 401, no admin block, no admin block, admin block |
| T05 | unit | ETag | `If-None-Match` same / old | 304 / 200 |
| T06 | bench | in-memory snapshot | `BenchmarkCapabilities` | p95 ≤ 20 ms; body ≤ 1024 bytes with 9 features |
| T07 | integration (Postgres) | two API replicas on one DB, an SSE client on each | `PUT` a component on replica A | both clients get `capabilities_changed` |
| T08 | unit | route table | `TestEveryRouteHasComponent` | every route mapped or core |
| T09 | unit | `livetv` off | `GET /api/channels/now` | 409 problem as §4 |
| T10 | unit | `livetv` off | `PATCH /api/rules/tv` as admin | 200 |
| T11 | unit | semantic off, gRPC fake that hangs | search | keyword hits in < 100 ms; `semantic: "off"` |
| T12 | unit (web) | `errorKind.test.ts` | 409 `component_off` | kind `off` |
| T13 | unit (Android, `client`) | `HomeError` parse | same | `ErrorKind.Off` |
| T14 | unit (Apple, MaktabaKit) | `ErrorKind.of` | same | `.off` |
| T15 | lint | `catalog` job | a feature without Arabic words | fails |

## Edge cases

- **`recommendations`.** §5 of the README lists no component behind the for-you rail, and no
  pipeline stage writes `user_recs` today. The feature is listed as always on until a component
  owns it; the feature list stays stable either way.
- **Licence lapses offline.** The licence store's grace (a 31-day licence refreshed daily, plus `SkewGrace`, 72 h; 51.19) keeps `Licensed` true, so `remote_access`
  stays on during it.
- **A newer server sends a feature an app doesn't know.** Ignored. A known feature missing
  from the answer counts as on (an older server).
- **Many changes at once** (a bundle switch) send one push, thanks to the 1 s coalescing.
- **A cloud-relayed client** gets the same answer through the relay; the payload is tiny.
- **Streaming's own routes** (`/api/stream/…`, channel playlists served by streaming) are
  outside the API middleware. A channel that is not running already answers there as today;
  the apps stop asking because `live_tv` is off.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/components/catalog.toml` | `features` on each component, as the table in §1. |
| `api/internal/components/features.go` (new) | Resolve features from the snapshot; ETag. |
| `api/internal/handlers/components/capabilities.go` (new) | `GET /api/capabilities`. |
| `api/internal/router/components_routes.go` (new) | `ComponentRoutes` table and the `component_off` middleware. |
| `api/internal/router/components_routes_test.go` (new) | `TestEveryRouteHasComponent`, T09, T10. |
| `api/internal/router/p51.go` | Mount capabilities and the middleware. |
| `api/internal/handlers/ws/ws.go` | `/ws/home` channel open to any signed-in user. |
| `api/internal/handlers/search/search.go` | Skip the semantic leg when `search.semantic` is off. |
| `web/src/lib/ws.ts` | `"home"` channel type. |
| `web/src/lib/errorKind.ts`, `errorText.ts` | Kind `off`. |
| `web/src/i18n/en.json`, `ar.json` | `error.kind.off`, `error.kind.offAdmin`, `components.feature.*`. |
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/api/HomeError.kt` | `ErrorKind.Off`. |
| `apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/Errors.kt` | Its sentence. |
| `apps/android/core/src/main/res/values/strings.xml`, `values-ar/strings.xml` | `error_off`, `error_off_admin`, `feature_*`. |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/HomeErrors.swift`, `ErrorStrings.swift`, `Localizable.xcstrings` | `.off` and its words. |
| `docs/wiki/api-catalog.md` | The route, the event and the problem code. |

### Tests

- T01–T06, T08–T11 in the Go `unit` job; T07 in `integration`.
- T12 in the web unit job; T13 in `android-apps` (JVM unit tests); T14 in `apple-apps`
  (MaktabaKit tests on macOS, CI only); T15 in `catalog`.

### Rollout

Ships with Phase 2, before 51.11. Apps that don't read capabilities yet already show the
`off` sentence for a `409` once they update.

### Rollback

`[components] capabilities_gate = off` turns the middleware into a no-op (routes behave as
today); the capabilities route keeps answering all `on`.

### Dependencies

51.08 (the service and its snapshot), 51.01 (catalog and the `catalog` job).
