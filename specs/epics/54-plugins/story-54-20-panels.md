# Story 54.20 — Panels declared as data, in every app

> Epic 54 · Phase 3 · Depends on: 54.06, 54.13, Epic 51 (51.11) · Needed by: —

## Why

Some plugin outputs fit no existing field: a loudness report, a list of detected scenes, a
"possible duplicates" table. Letting a plugin ship UI code would put third-party code in every
app on every platform, and a single image URL in that UI would be an exfiltration beacon. So a
plugin declares a **panel** as data, from a fixed set of blocks, and each app draws it with its
own native code.

## Description

### 1. Where panels appear

On the detail page of a **video**, **series** or **library**, in a "From plugins" section
below Maktaba's own sections, collapsed by default, one panel per plugin per page, at most 3
panels per page (admin-ordered).

### 2. The panel schema (`ui@1`)

`ui/panel.json` declares which `data` keys feed which blocks:

| Block | Shows | Limits |
|---|---|---|
| `kv` | key–value rows | ≤ 20 rows |
| `badges` | short labels with a tone (neutral, good, warning) | ≤ 10 |
| `chips` | tappable chips that run a Maktaba search for their text | ≤ 20 |
| `table` | columns + rows of plain text or numbers | ≤ 6 columns, ≤ 50 rows |
| `markers` | points on the video's timeline (seek on tap) | ≤ 200 |
| `image` | one artifact image the plugin wrote with `artwork.write` kind `panel` | ≤ 1 MB, served by the home |
| `text` | a paragraph | ≤ 1,000 chars |

Labels are keys into the plugin's `i18n` files. Values come from `plugin_data` for the
subject (54.04), keyed by the panel's declared keys. **Everything is plain text**: no URLs,
HTML, markdown or control characters. The API strips and refuses them when the plugin writes a
key a panel uses, and every app's renderer escapes again.

### 3. Who sees a panel

Decided 2026-11-03: **panels are shown to everyone in the home, viewers included,
once the plugin is installed.** There is no `audience` field and no separate viewer approval
step.

- Every user who can see the subject sees the panel. Viewers see it only on subjects in
  libraries they can access (normal ACL; a video they can't see is 404 as today).
- The guard is the store review: a person reviews every first submission and every version
  that adds a panel (54.16 §2), and the panel schema is read-only and data-only (below).
- The admin can still disable (keep off, 54.07) or uninstall the plugin at any time; its panels
  disappear on the next page load.
- A disabled, held or revoked plugin shows no panels to anyone.

### 4. API

`GET /api/panels?subject=video:<id>` answers the panels for the signed-in user: the schema,
strings in the user's language, and values. Filtered by library ACL and plugin phase (a
disabled, held or revoked plugin's panels disappear). ≤ 30 ms p95, ≤ 64 KB.

Apps learn whether to call it from `GET /api/capabilities` (Epic 51 51.09). Its `features` are
the catalog's top-level `features` list (nine today), each `"on"` or `"off"`. This story adds a
tenth, `plugin_panels`, to that list in `shared/components/catalog.toml` and lists it in the
`features` of `plugins.host`, so it is `"on"` while `plugins.host` is enabled and passes its
gates; its words are `components.feature.plugin_panels` in en and ar (web, Android
`R.string.feature_plugin_panels`, Apple `ErrorStrings.featureName`). A change moves the
capabilities ETag and sends `capabilities_changed` on `/ws/home` like any other. Epic 51's
client rule reads a known feature missing from the answer as `"on"` (an older server); for
`plugin_panels` the apps read a missing key as off and treat `404` from `/api/panels` as no
panels, so they never call an older server for nothing.

### 5. Every app

| App | Renderer |
|---|---|
| Web | `web/src/components/PluginPanel/` (React) |
| Apple (iOS, iPadOS, macOS, tvOS) | `PluginPanelView` (SwiftUI); tvOS shows `kv`, `badges`, `markers` only |
| Android (phone, tablet, TV) | `PluginPanel` (Compose); TV shows the same subset as tvOS |

Each renderer has a test that feeds it hostile values (URLs, HTML, very long strings, RTL
override characters) and asserts plain text, truncation and no network request.

### 6. As built

- `shared/plugins/panel.schema.json` documents `ui@1` (a `title`, 1–10 blocks; a block's
  `key` names a key of the plugin's data for the subject; `image` has no key and shows the
  PNG or JPEG set with `artwork.write` kind `panel`, at most 1 MB, served by
  `GET /api/panels/image`). `shared/plugins/pluginpanel` parses it (install refuses a bad file,
  `manifest_bad_panel`) and checks each value's shape and text on `data.put` of a key a
  panel uses: `plugin_text_forbidden`, `plugin_invalid` or `plugin_too_large`.
- `GET /api/panels?subject=<kind>:<id>&lang=` answers `{panels: [{plugin_id, plugin, title,
  blocks: [{type, label, columns, value, image}]}]}`: the plugin's words in the user's
  language (English when missing), plugins in install order (an admin order is not built yet),
  at most 3. The subject's library decides access (404 for a subject the user cannot see).
  A marker is "written after the change" when its data row is newer than the video's file
  `mtime`.
- Renderers: web `PluginPanel` (on the video page), MaktabaKit `PluginPanelView` and
  `PluginPanelsSection` (the Player's video screen), Android core `PluginPanel` and
  `PluginPanels` (the Player's video screen). The series and library pages and the TV apps
  take the same components in a follow-up. On Android, chips are shown, not tappable,
  until the Search route takes a query.
- T07 holds the median of 40 calls to 30 ms and logs the p95: on shared runners the tail
  measures the machine.

## Acceptance criteria

- **Given** a plugin with an admin panel on videos,
  **when** an admin opens a video it processed,
  **then** the panel shows under "From plugins" in web, Apple and Android apps, in the admin's
  language.
- **Given** the same panel and no approval step taken by anyone,
  **when** a viewer with access to the library opens the video,
  **then** the panel shows; a viewer without access gets 404 for the video as today.
- **Given** an admin disables the plugin,
  **when** a viewer reloads the video,
  **then** no panel is returned.
- **Given** a plugin writing `"https://evil.example/x.png"` into a key a panel shows,
  **when** written,
  **then** `400 plugin_text_forbidden`; and if a value gets past (fault injection), every app
  shows it as plain text and makes no request.
- **Given** the plugin is revoked,
  **when** the page reloads,
  **then** its panel is gone.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit ×2 (go, ts) | panel schema corpus | validate | per code |
| T02 | integration (PG, go) | roles × ACL × plugin phase | GET panels | shown to admins and viewers with access; none for a disabled, held or revoked plugin |
| T03 | unit (go) | text with URL, HTML, controls | write | refused |
| T04 | unit (web, Swift, Kotlin) | hostile values | render | plain text; no request (network spy) |
| T05 | snapshot (each app) | en / ar, dark theme | render | RTL correct; truncation |
| T06 | role test (go) | panels route | no one / viewer without access / viewer with access / admin | 401 / 404 / panels / panels |
| T07 | perf (go) | 3 panels | GET | ≤ 30 ms p95 |

## Edge cases

- **Older apps**: skip panels (capability false or unknown); nothing breaks.
- **A panel whose data keys are missing for this subject**: the panel is not shown.
- **Markers on a video whose file changed**: shown only if written after the change (content
  hash recorded with the data).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `shared/plugins/panel.schema.json` (new) | `ui@1`. |
| `api/internal/handlers/plugins/panels.go` (new) | §4. |
| `api/internal/plugins/pluginapi/routes_data.go` | Text rules for panel keys. |
| `web/src/components/PluginPanel/` (new) | Web renderer. |
| `apps/apple/.../PluginPanelView.swift` (new) + String Catalog `ar` | Apple renderer. |
| `apps/android/.../PluginPanel.kt` (new) + `values-ar` | Android renderer. |
| `shared/components/catalog.toml`, `web/src/i18n/{en,ar}.json`, Android and Apple strings | The `plugin_panels` feature and its words. |

### Tests

- Unit T01, T03, T04; integration T02; snapshots T05 (CI, dark theme; Apple and Android UI tests
  run in CI only); role T06; perf T07.

### Rollout

Behind the capability; apps ship renderers first, the server turns panels on after the apps'
releases reach TestFlight / Play internal testing.

### Rollback

Server answers `plugin_panels: false`; apps hide the section.

### Dependencies

54.06 (plugin data), 54.13 (the Plugins page, where an admin disables a plugin), Epic 51
51.11 (capabilities in apps).
