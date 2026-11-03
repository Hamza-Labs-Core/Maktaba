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

- `audience = "admins"` (default): admins only.
- `audience = "everyone"`: needs human store review (54.16 §2) **and** the admin's approval at
  install or later (a separate toggle on the plugin page). Then every user who can see the
  subject sees it. Viewers see values only for subjects in libraries they can access (normal
  ACL).

### 4. API

`GET /api/panels?subject=video:<id>` answers the panels for the signed-in user: the schema,
strings in the user's language, and values. Filtered by audience, approval, library ACL, plugin
phase (a held or revoked plugin's panels disappear). ≤ 30 ms p95, ≤ 64 KB. Capabilities
(Epic 51 51.09) gain `plugin_panels: true|false` so older apps skip the call.

### 5. Every app

| App | Renderer |
|---|---|
| Web | `web/src/components/PluginPanel/` (React) |
| Apple (iOS, iPadOS, macOS, tvOS) | `PluginPanelView` (SwiftUI); tvOS shows `kv`, `badges`, `markers` only |
| Android (phone, tablet, TV) | `PluginPanel` (Compose); TV shows the same subset as tvOS |

Each renderer has a test that feeds it hostile values (URLs, HTML, very long strings, RTL
override characters) and asserts plain text, truncation and no network request.

## Acceptance criteria

- **Given** a plugin with an admin panel on videos,
  **when** an admin opens a video it processed,
  **then** the panel shows under "From plugins" in web, Apple and Android apps, in the admin's
  language.
- **Given** the same panel,
  **when** a viewer opens the video,
  **then** no panel is returned by the API.
- **Given** a panel with `audience = "everyone"` approved by the admin,
  **when** a viewer with access to the library opens the video,
  **then** it shows; a viewer without access gets 404 for the video as today.
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
| T02 | integration (PG, go) | audiences × roles × ACL | GET panels | filtered correctly |
| T03 | unit (go) | text with URL, HTML, controls | write | refused |
| T04 | unit (web, Swift, Kotlin) | hostile values | render | plain text; no request (network spy) |
| T05 | snapshot (each app) | en / ar, dark theme | render | RTL correct; truncation |
| T06 | role test (go) | panels route | no one / viewer / admin | 401 / filtered / full |
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
| Epic 51 capabilities | `plugin_panels`. |

### Tests

- Unit T01, T03, T04; integration T02; snapshots T05 (CI, dark theme; Apple and Android UI tests
  run in CI only); role T06; perf T07.

### Rollout

Behind the capability; apps ship renderers first, the server turns panels on after the apps'
releases reach TestFlight / Play internal testing.

### Rollback

Server answers `plugin_panels: false`; apps hide the section.

### Dependencies

54.06 (plugin data), 54.13 (approval toggle on the plugin page), Epic 51 51.11 (capabilities in
apps).
