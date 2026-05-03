# Maktaba — Epics 11–17: Clients, UX, Discovery, Subscriptions

> Companion to [`specs/architecture.md`](../architecture.md). This document
> decomposes the client-surface, UX, networking-discovery, and monetization
> domains into epics, stories, acceptance criteria, test cases, and edge
> cases. Each story is sized for one focused engineering pass and is
> traceable up to a §-numbered architecture section.
>
> Conventions:
> - **AC** = Acceptance Criteria (binary checks the story passes/fails on).
> - **TC** = Test Cases (concrete scenarios; unit, integration, or e2e).
> - **EC** = Edge Cases (failure modes and boundary conditions).
> - All UIs are RTL-correct by default; all strings translatable; all video
>   bytes flow through the Streaming Service (`/stream/*`), never the API.
> - "Server" means the Maktaba backend (API + Streaming + Pipeline).

---

## Epic 11 — Web UI (React/Next.js PWA)

**Goal.** A single React 18 + TypeScript + Vite SPA that runs as the
canonical web client, gets installable as a PWA on iOS / Android / desktop,
and is the same code that ships inside the Capacitor (mobile) and Tauri
(desktop) shells. RTL-first for Arabic, fully keyboard-navigable, WCAG 2.1
AA, and offline-capable for everything except video bytes.

**Anchors:** architecture §6.2, §9 (REST + GraphQL + WS), §2.1 (web stack).

---

### Story 11.1 — Library browser (grid / list view, sorting, filtering)

The user lands on `/library` and can browse all videos in any library, with
poster grid by default, optional list view, server-side pagination, and
client-driven filter chips for language, content type, duration, speaker,
tag, and library.

**AC:**
- Grid view shows poster, title, duration badge, language flag, and
  processing badge (`PROCESSING`, `READY`, `FAILED`).
- List view shows the same fields plus filename, size, modified date, and
  state in a denser row.
- Toggling between grid and list is one click and persists in
  `localStorage` per user.
- Sorting options: title (asc/desc), recently added, recently watched,
  duration (asc/desc), language. Default: recently added.
- Filter chips: language (multi), content type (multi), duration buckets
  (`<10m`, `10–30m`, `30–60m`, `1h+`), speaker (typeahead), tag (multi),
  library (single). Filters are URL-encoded so views are linkable.
- Cursor pagination: "Load more" sentinel triggers `?cursor=...&limit=60`;
  spinner overlays the existing grid until the next page arrives.
- Empty states: "Library is empty" with primary CTA "Scan now" if the user
  is admin; "No videos match these filters" with a "Clear filters" CTA.

**TC:**
- Render 1,000-video library: first paint ≤ 1.5 s on a cold cache;
  pagination scrolls smoothly at 60 fps on a 2019 MacBook Air.
- Filter by `language=ar` + `type=lecture` → URL becomes
  `?lang=ar&type=lecture`; deep-linking that URL reproduces the same view.
- Switch grid → list → grid: scroll position is preserved.
- Apply a filter mid-pagination: the cursor is reset and the grid re-fetches
  from page 0.

**EC:**
- A library returns 0 results because the user filtered on a tag that has
  been deleted server-side: surface a non-blocking toast and clear that
  chip from the URL.
- The poster URL 404s (cache evicted): show the placeholder poster, log
  client warning, do not break the row.
- Slow network mid-pagination (>5 s): show "Still loading…" inline.
- Mixed-direction titles (Arabic + English): titles render with
  `unicode-bidi: isolate` so neither direction bleeds into the other.

---

### Story 11.2 — Video detail page (metadata, subtitle tracks, processing status)

A `/watch/{id}` page that shows the full video metadata, available
subtitle/audio tracks, processing job state, transcript sidebar, chapter
list, and the player itself. Renders with partial data: a video that's
`PROBED` but not yet `TRANSCRIBED` still shows player + metadata, marks the
transcript as "in progress", and live-updates as segments arrive over WS.

**AC:**
- Header: title, poster, duration, language flags, content type, library
  name, file path (admin-only).
- Tabs: **Watch** (default), **Transcript**, **Chapters**, **Files**,
  **Processing**.
- The Processing tab shows every `processing_jobs` row for this video,
  current state, last segment offset, ETA, and per-stage controls
  (pause/resume/cancel/retry).
- Subtitle track list shows source (auto / sidecar / embedded), language,
  format (`srt` / `vtt`), and "set as default" affordance. Default applies
  to all clients via `playback_state.preferred_subtitle_lang`.
- Audio track list shows codec, channels, language, and "play this track"
  affordance.
- Live updates over `/ws/jobs` and `/ws/library/{id}` re-render badges and
  progress bars without a full refetch.

**TC:**
- Open a video that's still transcribing at 23%: the transcript sidebar
  shows the segments persisted so far; new segments slide in as
  `job.progress` events arrive.
- Switch subtitle track from Arabic-auto to English-sidecar: the new VTT
  loads within 1 s; player position is preserved.
- Admin clicks "Reprocess from `transcribe`" on the Processing tab: a
  confirmation modal appears; on confirm, the segments are cleared and a
  new job is enqueued.
- Non-admin user tries to see file path: hidden behind a feature gate;
  no information leak.

**EC:**
- Job is in `failed` state with a long error message: the message is
  truncated with a "Show details" disclosure; copy-to-clipboard works.
- A subtitle file referenced in the DB has been deleted on disk: the row
  is greyed out with a "File missing — rescan needed" hint.
- Video state is `failed` with no recoverable jobs: the player surface
  shows "Cannot play — file missing or unreadable" instead of attempting
  a session.
- Transcript exceeds 50,000 segments (very long lecture): virtualize the
  list (TanStack Virtual); first paint ≤ 500 ms.

---

### Story 11.3 — Video player (HLS.js / Vidstack, subtitle overlay, chapter nav, speed control)

The player is Vidstack with HLS.js fallback for environments where native
HLS isn't available. It plays the manifest URL minted by `POST
/api/stream/sessions`, renders auto and sidecar subtitles, exposes chapter
markers on the scrubber, and supports playback rates 0.5×–2× in 0.25
increments.

**AC:**
- Player loads within 2 s of the user clicking Play (cold start) on a
  100 Mbps LAN.
- HLS adaptive bitrate switches happen invisibly; overlay shows current
  rendition only when the user enables it via Settings → Show stats.
- Subtitle overlay supports Arabic correctly: RTL text rendering, Arabic
  numerals, no ligature breakage. Style controls (size, opacity, color,
  background) apply live.
- Chapter markers are rendered as ticks on the scrubber; hover shows the
  chapter title; click jumps to `chapter.start_sec`.
- Speed control: 0.5×, 0.75×, 1×, 1.25×, 1.5×, 1.75×, 2×. Audio pitch
  preserved (player default).
- Keyboard: `Space` toggle play, `←/→` ±10 s, `Shift+←/→` ±30 s, `J/L`
  ±10 s, `K` toggle play, `M` mute, `,` / `.` previous/next chapter, `0–9`
  jump to N×10%, `F` fullscreen, `C` toggle subtitles, `+/-` speed.
- Picture-in-picture: dedicated button + browser API; survives navigation
  away from `/watch/{id}`.
- Watch progress: posted every 10 s and on pause/seek; resume offer
  appears next time the user opens the same video on any device.

**TC:**
- Start a 1-hour video at 0; seek to 35:00. Stream catches up within 3 s.
- Play a 4K HEVC source on Safari (no native HEVC): the API returns a
  transcoded HLS session; the player consumes it without intervention.
- Toggle subtitles off and on while playing: no playback hiccup.
- Open the same video on a second tab: WS broadcasts position, the new tab
  shows "Resume at 35:14".
- Network drops for 8 s mid-segment: HLS.js back-off retries; UI shows a
  spinner overlay; recovery is automatic.

**EC:**
- Manifest expires mid-watch (`expires_at` < now): client refetches
  `POST /api/stream/sessions` transparently; the gap is < 1 s.
- User changes audio track: the existing session is closed
  (`DELETE /api/stream/sessions/{id}`) and a new one opened; resume
  position is preserved.
- Browser blocks autoplay with sound: player starts muted with a
  "Click to unmute" affordance.
- HLS.js fails to bootstrap (e.g., quota for media source): fall back to
  direct play if the source is browser-compatible; otherwise show a
  recoverable error with a "Retry" button.
- Source duration in metadata disagrees with the manifest: trust the
  manifest's `EXT-X-ENDLIST` for end-of-content detection.

---

### Story 11.4 — Search interface (instant search, faceted filters, time-coded results)

A `/search` page (and a header search box on every other page) that
debounces user input, hits `POST /api/search` with hybrid mode by default,
shows highlighted snippets with timestamp deep-links, and supports facet
filters (language, library, speaker, content type, date range).

**AC:**
- Header search box debounce: 200 ms; suggestions appear in a dropdown
  using `GET /api/search/suggest?q=...`.
- Results page shows hits grouped by video, with per-hit snippet (≤ 200
  chars) and a clickable timestamp like `[06:12 → 06:24]` that opens
  `/watch/{id}?t=372`.
- Facet sidebar: counts per facet are returned in the search response and
  rendered as collapsible groups.
- Mode toggle: FTS / Semantic / Hybrid (default). Persists per user.
- Empty state: "No matches for «query»" with suggestions ("did you mean…",
  pulled from `suggest`).
- Saved searches: a "Save this search" button stores the current query +
  filters via `POST /api/search/save`; saved searches appear in the
  sidebar under "My Searches".
- The result list virtualizes — 1,000 hits paint smoothly.
- Highlighted spans use `<mark>` and respect bidi (Arabic queries
  highlight Arabic substrings, not Latin lookalikes).

**TC:**
- Type `الحمد لله` in the box: results stream in < 500 ms on the household
  scale (15,000 hours indexed). Top hit's timestamp deep-link plays the
  exact second.
- Switch mode FTS → Semantic on the same query: the result set may differ;
  the URL updates with `?mode=semantic`.
- Type a 1-character query: no request fires (min length 2 by default).
- Save search "Sermons mentioning تفسير ≥ 30 min": appears under My
  Searches; clicking reproduces the URL.

**EC:**
- Server returns `total: 0` but suggestions: surface the suggestions
  prominently.
- Query contains unbalanced quotes or FTS5-illegal characters: client
  sanitizes by escaping rather than failing.
- Backend search timeout (>5 s): show "Search took too long, retry?" with
  a Retry button; do not silently swap to a partial index.
- A very long query (> 1 KB): client refuses to submit (HTTP 400 prevented
  client-side) with an inline message.
- Mixed-script query (Arabic + English): RTL/LTR fragments rendered with
  isolates; suggestions don't reorder the user's typed characters.

---

### Story 11.5 — Processing queue dashboard (progress bars, pause / resume controls)

A `/queue` page that renders the live worker pool: jobs grouped by stage,
per-job progress bar, ETA, retry / pause / resume / cancel actions. Backed
by `GET /api/jobs`, `WS /ws/jobs`, and `GET /api/queue/stats`.

**AC:**
- Page loads with the current state of all jobs in the last 24 hours by
  default; "Show all" expands history.
- Each job row: video poster + title, stage badge, state badge, progress
  bar with `processed_seconds / total_duration_seconds`, ETA, attempts
  counter.
- Inline actions per job: Pause, Resume, Cancel, Retry (Retry only on
  `failed`), "Move to front of queue" (priority bump).
- Bulk actions: select multiple jobs → Pause all / Resume all / Cancel all.
- Per-stage cards at the top: pending / running / done / failed counts;
  click filters the list.
- Live updates: WS `job.progress` events update the progress bar without
  a refetch; UI throttles renders to ≤ 1 Hz per visible job (architecture
  §7.10).
- Force-pause: when a job is stuck in `pause_requested = true` for >
  `pause_grace_sec`, a "Force pause" button appears (architecture §7.7).
- Empty state: "No jobs running" with a "Process all unindexed videos" CTA
  for admins.

**TC:**
- 50 jobs running in parallel: the page stays responsive; CPU < 10% on the
  client.
- Click Pause on a transcribe job: within 60 s the job enters `paused` at
  the next segment boundary; the row re-renders with the resume offset.
- Pause then immediately Resume: idempotency holds — the job either
  resumes from the paused offset or, if pause hasn't taken effect yet,
  ignores the resume (resume on a `running` job is a no-op).
- Disconnect WS for 15 s, reconnect: the dashboard does a one-shot
  re-fetch and reconciles state.
- Retry a `failed` job: `attempts` resets to 0, state goes `pending`.

**EC:**
- A job's `last_segment_end_sec` decreases between snapshots (server bug):
  detect and log; do not animate the bar backwards.
- ETA jitters because `realtime_factor` is unstable on a fresh job: don't
  show ETA until at least 3 segments have committed.
- 1,000+ jobs: the list virtualizes; bulk-select uses a server-side filter
  expression rather than a client-side ID list.
- Network partition during a bulk Pause: show "12 of 50 paused — retry the
  rest?" rather than a silent partial success.

---

### Story 11.6 — Settings page (STT engine config, library paths, user preferences)

A `/settings` route with sections: Libraries, STT Backends, Search,
Playback, Account, Appearance, About. Backend-driven (`GET /api/settings`,
`PATCH /api/settings`, `GET /api/settings/stt-backends`).

**AC:**
- Libraries section: add / edit / delete (with `purge=true|false`
  confirmation), set roots, language, STT profile, scan now button.
- STT Backends section: list available (`whisper-mlx`, `whisper-cpu`,
  `whisper-cuda`, `openai-api`); show health (`OK` / `unavailable`);
  per-backend config (model size, monthly cap for paid backends).
- "Test" button on each backend runs `POST /api/settings/stt-test` and
  shows latency / sample output.
- Search section: hybrid weights (0.0–1.0 sliders for FTS / semantic),
  default mode, segment grouping, default top-K.
- Playback section: default subtitle language, default audio language,
  default playback rate, default quality cap (`Auto`, `1080p`, `720p`,
  `480p`), data-saver toggle (mobile only).
- Account section: change password, list active sessions (with revoke),
  PAT (Personal Access Token) management for clients.
- Appearance section: theme (Light / Dark / System), UI language (Arabic
  / English), density (Comfortable / Compact).
- About section: server version, build, uptime, license, link to
  changelog.

**TC:**
- Admin adds a new library at `/mnt/films`: a `POST /api/libraries`
  succeeds; "Scan now" enqueues a scan job; UI updates within 2 s.
- Switch STT backend on `Films` library from `whisper-mlx` to
  `openai-api`: a confirmation modal warns about cost; on confirm, future
  scans use the new backend, in-flight transcriptions continue on the old.
- Set monthly cap to $10, then issue a job that would exceed it: backend
  refuses claim; UI surfaces "Budget exceeded — bump cap or wait until
  next cycle".
- Change UI language to Arabic: layout flips to RTL within one route
  transition; previously visited views re-render correctly.

**EC:**
- A path that doesn't exist on the server: server returns 422 with a
  problem+json `path-not-found`; UI surfaces inline next to the field.
- Two admins editing the same library concurrently: optimistic lock via
  `If-Match` on `updated_at`; second write returns 409, UI offers
  "Reload and merge".
- Removing the only admin's password without setting a new one: backend
  refuses; UI shows the rule in advance.
- Test STT backend with no audio sample available on the server: the
  endpoint returns "no test fixture installed" — show a "Run smoke
  transcribe on any 30-second video" affordance instead.

---

### Story 11.7 — Responsive design (desktop, tablet, mobile)

The same React app must render correctly from 360 px (phone portrait) to
2560 px (large desktop), using a mobile-first Tailwind breakpoint scale
and never horizontally scrolling at supported sizes.

**AC:**
- Breakpoints: `sm 640`, `md 768`, `lg 1024`, `xl 1280`, `2xl 1536` (Tailwind
  defaults).
- At ≤ 640 px the navigation collapses to a bottom tab bar (Library, Search,
  Queue, Settings); the header search becomes a full-screen overlay.
- At 641–1023 px the sidebar collapses to icons; tap to expand.
- At ≥ 1024 px the full sidebar is permanent.
- Video player layout: full-width 16:9 below 768 px; 16:9 with a
  side-by-side transcript at ≥ 1024 px.
- All text scales correctly at 200% browser zoom; no horizontal scroll
  appears at any breakpoint.
- Touch targets ≥ 44 × 44 CSS px on touch devices.

**TC:**
- Viewport-test matrix: iPhone SE 375 × 667, Pixel 7 412 × 915, iPad 1024
  × 768, MBP 14 1512 × 982, 4K 2560 × 1440. Visual regression suite (Playwright
  + image diff) gates merges.
- Rotate iPad while playing video: the player layout reflows without
  pausing.
- Browser zoom 200% on desktop: layout still readable, no overflow.

**EC:**
- Foldable Android (split mode 280 px wide): graceful, never broken; show
  a "Maktaba is best at ≥ 320 px wide" hint.
- Browser without `container queries` support: fall back to media queries
  (Tailwind already does so).
- Extreme aspect ratios (ultra-wide 21:9): video letterboxed,
  transcript sidebar wider — never stretches the player.

---

### Story 11.8 — Dark / light theme

Theme is `light`, `dark`, or `system` (default). Switching is instant, no
flash of incorrect theme on cold load.

**AC:**
- Theme tokens are defined as CSS custom properties on `:root[data-theme]`.
- `system` honors `prefers-color-scheme` and updates live when the OS
  toggles.
- Theme persists in `localStorage` and is applied before first paint
  (inline blocking script in `index.html`) — no FOUC.
- Posters and thumbnails render correctly on both themes; subtitles
  contrast against the player background regardless of theme.
- All components meet WCAG 2.1 AA contrast in both themes (see Story
  11.11).
- Switching theme animates background and surface colors over 150 ms; no
  layout shift.

**TC:**
- Toggle from light → dark → system on macOS and switch macOS theme:
  Maktaba's UI follows.
- Boot the page with `localStorage` empty and `prefers-color-scheme:
  dark`: app boots dark with no flash.
- Print stylesheet: prints in light theme regardless of UI selection.

**EC:**
- A user-supplied custom CSS overrides a token: warn in DevTools console
  but do not crash.
- Theme key in `localStorage` is corrupted (`"darkk"`): fall back to
  `system`.
- High-contrast OS mode (Windows): forced colors are honored where
  applicable; we do not override `forced-colors: active`.

---

### Story 11.9 — Keyboard shortcuts

A global shortcut layer with a help overlay (`?`) that lists every
shortcut.

**AC:**
- Shortcuts:
  - `g l` → go Library, `g s` → Search, `g q` → Queue, `g h` → Home, `g ,` →
    Settings.
  - `/` → focus header search.
  - `?` → toggle keyboard help overlay.
  - In player: see Story 11.3.
  - `j / k` on lists → move focus next/previous; `Enter` → open.
- Shortcuts are disabled when any text input is focused (except global
  `Esc`).
- Help overlay is filterable, RTL-aware, and lists per-context shortcuts
  (player, list, search).
- Shortcuts honor RTL: `←/→` semantics in the player flip in RTL mode so
  `→` is "back" in Arabic UI (configurable: "use logical arrows" toggle in
  settings).

**TC:**
- Press `g l` from the Home page: navigates to Library within 200 ms.
- Press `?` while focused in the search box: nothing happens (input is
  active).
- Press `?` from anywhere else: overlay opens.
- Hold `g` for 2 s without a follow-up: nothing happens; the leader is
  silently dropped.

**EC:**
- IME composing text (Arabic, Japanese): shortcuts disabled until
  composition ends.
- A shortcut conflicts with a browser shortcut (e.g., `Ctrl+S`): the
  Maktaba shortcut should not preempt the browser default.
- On Linux/Windows, `Cmd` is not present: documentation maps to `Ctrl`
  uniformly.

---

### Story 11.10 — Offline capability (PWA service worker)

A Workbox-driven service worker caches the app shell and recently-fetched
metadata; offline, the user can browse what they've already seen and
queue actions for later sync.

**AC:**
- Service worker is registered after first paint (no blocking).
- App shell (HTML, JS, CSS, fonts) cached with `cache-first`, max age 30
  days, busted by a build hash.
- Library list, video metadata, search results: `stale-while-revalidate`,
  TTL 5 min.
- Video bytes: never cached by the SW; the player handles its own buffer.
- Offline UI: a banner "You are offline — showing cached results"; actions
  that require the network (start a session, save a search) are queued
  with `bgsync` and replayed on reconnect.
- "Install Maktaba" prompt is shown once at session 3+, dismissable.
- Update flow: on a new SW version, show "An update is available — Reload"
  toast.

**TC:**
- Offline + previously-visited library: the grid renders from cache;
  poster images appear from cache.
- Offline + never-visited library: shell renders, content area shows
  offline empty state.
- Replay queue: queue 3 "save search" actions offline → reconnect → all 3
  fire in order; if one returns 409, the others still apply.
- SW update test: deploy build B → existing tab gets a "Reload" toast;
  reload picks up B; the old SW is unregistered cleanly.

**EC:**
- Quota exhaustion (Safari ITP, 50 MB per origin): SW stops caching new
  responses with `quotaexceeded`; UI surfaces "Offline cache full — some
  data may be missing" non-blocking.
- iOS Safari quirk: SW killed after 30 s idle. We do not rely on long-lived
  workers for any critical path.
- A request to `POST /api/auth/login` is never queued (security: an
  offline replay of a login is meaningless); only idempotent or
  user-initiated state changes are queued.

---

### Story 11.11 — Accessibility (WCAG 2.1 AA)

Every page meets WCAG 2.1 AA contrast, keyboard navigation, focus order,
ARIA semantics, screen-reader compatibility, and reduced-motion support.

**AC:**
- All interactive controls have visible focus rings (≥ 3:1 contrast)
  matching the design tokens.
- All images have `alt` attributes; decorative images use `alt=""`.
- Color is never the sole carrier of meaning (state badges include text
  + icon).
- `prefers-reduced-motion` disables non-essential animation.
- Form fields have `<label>` associations; errors announced via
  `aria-live="polite"`.
- Skip-to-content link at the top of every page.
- Player exposes ARIA roles for play/pause/seek and announces time updates
  via `aria-valuetext`.
- Subtitles can be enabled/disabled with a single keyboard action and are
  announced.
- Automated axe-core scan in CI: 0 violations on every page.
- Manual VoiceOver / NVDA pass each release; checklist documented in
  `docs/a11y.md`.

**TC:**
- Run axe-core on each route: 0 serious or critical violations.
- VoiceOver navigates `/library` end-to-end without trapping or skipping
  posters.
- A user with reduced motion sees no chrome animations on theme change.
- Tab order on `/watch/{id}` is: Header → Player → Transcript sidebar →
  Footer; `Shift+Tab` reverses correctly.
- Color-blind simulator (Protanopia / Deuteranopia / Tritanopia): no UI
  state becomes ambiguous.

**EC:**
- A third-party widget (player) with a known a11y issue: we wrap it with
  ARIA scaffolding and document the deviation; we do not silently regress.
- Browser zoom 400% (WCAG 1.4.10 reflow): single-column layouts; no
  horizontal scroll except for charts.
- Screen reader on a transcript with 10,000 segments: virtualized list
  exposes only ±20 items at a time but supports `role="feed"` with
  `aria-busy` during fetches.

---

### Story 11.12 — i18n (Arabic RTL + English LTR)

The same React app ships in Arabic and English at parity; adding a third
locale requires only translation files, not code changes.

**AC:**
- Locale detection: URL `/ar/...` or `/en/...` if present, else
  `Accept-Language`, else `en` (configurable per server).
- All strings live in `web/src/i18n/{locale}.json`; no string is inlined
  in JSX.
- Arabic uses `dir="rtl"` and Arabic numerals by default (configurable).
- All layouts are mirrored under RTL: navigation chevrons flip, padding
  asymmetries flip, scrollbars are on the appropriate side.
- Date / time / number formatting via `Intl.DateTimeFormat` /
  `Intl.NumberFormat` with the active locale.
- Mixed-direction strings render with `unicode-bidi: isolate` and use
  Unicode bidi-isolate characters (`⁨...⁩`) where escapes are needed in
  templates.
- Transcript snippets in search results render in their source language
  even when the UI is in the opposite direction.
- Pluralization handled via ICU MessageFormat (`{count, plural, one {…}
  other {…}}`).

**TC:**
- Switch UI to Arabic: the entire shell flips RTL; no element is
  visually clipped.
- Search in Arabic for English content: hits render LTR snippets inside
  an RTL container without bleeding direction.
- Number formatting: `1234567` displays as `1٬234٬567` in Arabic locale,
  `1,234,567` in English.
- Translation key missing in Arabic: falls back to English with a console
  warning in dev; no broken UI.

**EC:**
- A translation expands by 70% (German placeholder for tests): layouts use
  `min-content` / `auto` and don't truncate.
- Arabic fonts not loaded yet: use a `font-display: swap` fallback that
  renders correctly in both directions.
- A right-aligned scrollbar in RTL conflicting with player chrome: chrome
  is anchored to logical-end, not physical-right.

---

## Epic 12 — Mobile Apps (Capacitor)

**Goal.** iOS and Android apps that wrap the same web bundle as a native
shell, with a native-player handoff plugin, background download, push
notifications, share targets, and Keychain/Keystore for refresh tokens.
The web app is the source of truth for UI and routing; native code only
exists where browser APIs are insufficient.

**Anchors:** architecture §6.3, §2.1 (Capacitor 6).

---

### Story 12.1 — iOS app wrapper

A Capacitor 6 wrapper that builds an iOS app from `web/`, launches a
native shell, and handles iOS-specific lifecycle events (background /
foreground, low memory, status bar).

**AC:**
- App targets iOS 16+; iPhone and iPad universal.
- Splash screen matches the brand; cold launch to library list ≤ 3 s on
  iPhone 13.
- Status bar style follows the active theme; safe-area insets respected
  on every notched / Dynamic Island device.
- Backgrounding pauses non-essential timers (WS reconnect throttles to
  60 s); foregrounding refreshes the visible screen.
- Memory warnings: clear in-memory caches, keep state.
- App icon, launch image, and dark-mode variants present.
- TestFlight build pipeline configured; signing via the provisioning
  profile in `apps/mobile/ios/`.

**TC:**
- Launch on iPhone SE (smallest current screen): no clipping, all CTAs
  accessible.
- Launch on iPad with split view: layout adapts (treats as tablet).
- Background → 30 minutes → foreground: WS reconnects, UI shows fresh
  data within 1 s.
- Low-memory simulation: caches purged, no crashes.

**EC:**
- WKWebView crash on a malformed video URL: native shell catches and
  reloads the route with an error banner instead of white-screening.
- App killed mid-download: see Story 12.6.
- iOS 16.0 specifically (older WKWebView quirks): tested explicitly; any
  workaround documented.

---

### Story 12.2 — Android app wrapper

A Capacitor 6 wrapper that builds an Android APK / AAB with the same web
bundle.

**AC:**
- App targets Android 9+ (API 28); ARM64 + ARMv7.
- Cold launch to library list ≤ 4 s on a Pixel 5.
- Edge-to-edge layout with proper insets on notched / hole-punch devices.
- Back button: pops the in-app history stack; from the root, prompts
  "Quit Maktaba?".
- Foreground service for downloads (Story 12.6) declared in manifest.
- Play Store internal testing track configured; AAB signing via Play App
  Signing.

**TC:**
- Launch on a low-end Android (Moto G play): start ≤ 6 s, no ANR.
- Tap Back from Settings: returns to the previous tab.
- Background → 1 hour → foreground: data refreshes; no stale spinner.
- Rotate device on the player route: video continues without restarting.

**EC:**
- WebView updated mid-session (Chrome System WebView background update):
  app survives the implicit reload.
- A device with no Google Play Services (e.g., Huawei post-2019): push
  falls back to in-app polling; downloads work via WorkManager.
- Sideloaded APK with no Play Store: in-app updater (we ship our own
  update check pinging `/api/system/version`) prompts for manual install.

---

### Story 12.3 — Native video player integration

A Capacitor plugin (`plugins/native-player/`) that opens the system
AVPlayer (iOS) or ExoPlayer (Android) for full-screen playback. The web
player is used for inline (non-fullscreen) playback; tapping fullscreen
hands off to native for AirPlay / Cast / PiP.

**AC:**
- Plugin API: `nativePlayer.open({manifestUrl, posterUrl, title, startSec,
  audioTrack?, subtitleTrack?})` → returns a session handle.
- iOS uses `AVPlayerViewController` with `AVPlayerItem`; Android uses
  `ExoPlayer` with `MediaItem`.
- Now Playing metadata: title, poster, duration published via
  `MPNowPlayingInfoCenter` (iOS) and `MediaSession` (Android), so lock
  screen and AirPods controls work.
- Subtitle track switching is exposed in the native player UI; selection
  echoes back to the web layer via plugin event.
- Closing native player returns the user to the in-app
  detail page with the latest position synced.

**TC:**
- Tap fullscreen on iPhone, swipe to AirPlay: stream continues on Apple
  TV; play/pause from the iPhone control center works.
- Lock the device mid-playback: the lock screen shows poster + scrubber.
- Cast from Pixel to a Chromecast: ExoPlayer supports the
  cast-discover-session flow; switching back to the device resumes
  position.
- Position sync: native player reports every 10 s to `/api/stream/sessions/{id}/progress`.

**EC:**
- HLS source's audio language list disagrees with the manifest selection:
  prefer the manifest, log a warning.
- AirPlay receiver can't decode HEVC sidecar: the API generates a
  compatibility transcode; we never attempt to push an unsupported codec.
- Background audio mode (audio continues after lock): see Story 12.5.
- User force-closes the app mid-stream: server-side session reaper
  (architecture §4.2) closes the slot within 90 s.

---

### Story 12.4 — Push notifications (processing complete, new content)

APNs (iOS) and FCM (Android), bridged through the API. Notifications are
strictly opt-in.

**AC:**
- First-launch flow: ask for notification permission only after the user
  enters the Queue page or finishes onboarding (Story 17.6); never on
  first paint.
- Categories: "Processing complete", "New content added", "Job failed",
  "Subscription expiring" (if applicable, see Story 16.x).
- User can toggle each category in Settings → Notifications.
- Notifications carry a deep-link payload (`maktaba://watch/{id}`) and
  open directly to the right page (Story 12.9).
- Token registration: client posts the device token to
  `POST /api/devices/register {token, platform, locale}`; server stores
  per-user.
- Token rotation: client refreshes on every cold launch; server dedupes.
- Notification sound and vibration follow the device defaults.

**TC:**
- Process a 4-hour video; on completion, the user receives a notification
  within 30 s. Tapping opens the video detail page.
- Toggle off "New content" notifications: the next library scan does not
  emit a notification.
- Revoke notification permission at the OS level: the in-app settings
  reflect this within one app launch.

**EC:**
- APNs / FCM rate-limits (e.g., 100/sec/user): server batches into a
  single "5 jobs completed" notification.
- Token invalidated server-side (user logged out): notifications are
  silently dropped; client re-registers on next login.
- Locale mismatch (server speaks Arabic, device speaks English):
  notifications use the device locale via the `locale` field at
  registration time.
- Quiet hours (a future setting): notifications respected on the server,
  not the device, so they're consistent across platforms.

---

### Story 12.5 — Background playback

Audio continues playing when the screen is off or the user switches apps,
on both iOS and Android, with system controls for play/pause/seek.

**AC:**
- iOS: audio session category `.playback`, `audioMode = .moviePlayback`.
- Android: foreground service of type `mediaPlayback` with a persistent
  notification.
- Lock-screen / notification-shade controls: play/pause, seek ±10 s,
  next/previous chapter, scrubber.
- Pulling out headphones pauses; reconnecting does not auto-resume
  unless the user has set "Auto-resume on headphone reconnect" in
  Settings → Playback.
- Picture-in-picture (PiP) supported on iPad and Android; auto-engaged on
  swipe-to-home if the user has enabled it.
- Background tasks: position sync every 10 s; resilient to brief
  network drops (≤ 30 s).

**TC:**
- Start a lecture, lock the iPhone: audio continues; lock screen shows
  controls.
- Tap PiP on iPad mid-playback, switch to Notes: the player floats; tap
  it to expand back.
- Headphone unplug pauses; "Auto-resume" off → manual play required.
- WebSocket disconnects in background: reconnects on foreground; does not
  spam reconnect attempts in background.

**EC:**
- iOS bans background WebSocket beyond ~30 s: position sync uses
  background-fetch URLSession instead.
- Android Doze mode: the foreground service exempts us; position sync
  continues. We never claim a wake lock.
- Bluetooth latency causes seek desync: native player handles its own
  resync; we do not double-correct.

---

### Story 12.6 — Download for offline viewing

The user can mark a video for offline download; the file (and its
subtitles + poster) is downloaded to encrypted device storage and
playable when offline. Downloads are pause-resumable and survive app
suspension.

**AC:**
- "Download" action on every video detail page; per-quality picker
  (1080p / 720p / 480p / Audio-only).
- iOS: `URLSession` background tasks; Android: WorkManager + DownloadManager.
- Download status surface: progress bar, pause/resume/cancel; a "Downloads"
  tab lists all current and completed downloads.
- Storage quota: configurable cap (default 5 GB) with an LRU eviction
  policy; users can pin items to prevent eviction.
- Encryption at rest: per-app sandboxed storage on iOS; Android scoped
  storage (`MediaStore.Downloads/Maktaba/`) with file-level encryption
  via the device's Keystore-derived key.
- Offline playback: when offline, the detail page shows the local file as
  the source; subtitle tracks load from local sidecar.
- Sync: marking a video downloaded sets a server-side flag so other
  devices see "downloaded on Phone".

**TC:**
- Download a 2 GB video on Wi-Fi only: download proceeds; switching to
  cellular pauses (configurable).
- Resume a partial 800 MB download after an app kill: HTTP Range request
  resumes from the byte offset.
- Watch the video offline: player loads the local URL; progress syncs to
  the server when network returns.
- Cap exceeded (5.5 GB requested with 5 GB cap): the oldest unpinned
  download is evicted with a confirmation banner.

**EC:**
- Cellular data limit hit mid-download: pause, surface "Resume on Wi-Fi?".
- File integrity: each completed download is BLAKE3-checksummed against
  the server's `content_hash`; mismatch → discard and re-download.
- App reinstall: downloads are lost (sandbox cleared); UI resets the
  download flag.
- iOS background download budget exhausted: the system pauses and
  resumes when conditions allow; we surface "Will resume when conditions
  allow" rather than failing.

---

### Story 12.7 — Share / AirPlay / Chromecast support

Native share sheet and casting on both platforms.

**AC:**
- "Share" button on video detail: opens the native share sheet with a
  deep link `https://{server}/watch/{id}` and a fallback poster.
- AirPlay: integrated via AVPlayer; AirPlay button visible in player
  controls when the device sees a receiver.
- Chromecast: integrated via the Cast SDK; Cast button visible when a
  receiver is on the LAN. Cast session published to `MediaSession`.
- Share to Messages, Mail, Notes, third-party apps: all use the same
  metadata payload (URL + title + poster).
- Receiving a shared link in another Maktaba app opens the deep link
  (Story 12.9).

**TC:**
- Share to Messages: link previews with poster and title.
- AirPlay during playback: receiver picks up the stream within 3 s; local
  device shows "Now playing on Apple TV".
- Chromecast on a network with two receivers: picker lists both;
  selection persists for the session.

**EC:**
- Receiver doesn't support the source codec: we fall back to a
  HLS-transcoded stream (architecture §4.1 mode 3).
- AirPlay 2 multi-room: only the primary room is targeted (multi-room
  audio is out of v1 scope).
- Cast session lost mid-playback (receiver power-cycled): we surface a
  toast and offer "Resume on this device".

---

### Story 12.8 — Haptic feedback

Light haptic cues on key user actions, respecting the OS-level haptics
toggle.

**AC:**
- Haptic events:
  - Tap a navigation tab → light tap.
  - Long-press a video card → medium impact.
  - Toggle a setting → selection-change.
  - Download complete → success notification haptic.
  - Error toast → warning notification haptic.
- iOS uses `UIImpactFeedbackGenerator` and `UINotificationFeedbackGenerator`;
  Android uses `HapticFeedbackConstants`.
- Respect "Reduce motion / haptics" in the OS settings.
- Configurable in Settings → Accessibility → Haptics (Off / Light / Full).

**TC:**
- iOS with haptics disabled in Settings: no haptics fire.
- Long-press card on Android: feels distinct from a tap.
- Error haptic does not fire on a routine validation error (only on
  network/server error).

**EC:**
- Devices without haptics (older Android tablets): silently no-op.
- Rapid-fire actions (typing in search): haptics throttled to ≤ 1 every
  100 ms.

---

### Story 12.9 — Deep linking

Universal links / app links and a custom scheme `maktaba://` that opens
the app to a specific video, search, collection, or settings page.

**AC:**
- iOS Universal Links: `https://{server}/watch/{id}` → app, with web
  fallback if not installed.
- Android App Links: same scheme via
  `assetlinks.json` published from the server at `/.well-known/`.
- Custom scheme `maktaba://watch/{id}?t=...` for in-app inter-route
  navigation and notification payloads.
- Deep links to: `/watch/{id}?t=...`, `/search?q=...`, `/library`,
  `/library/{id}`, `/queue`, `/settings`, `/collection/{id}`.
- Cold launch via deep link goes to the deep-linked route, not the home
  page.
- Authentication: if the user is not logged in, deep link is preserved
  and replayed after login.

**TC:**
- Tap `https://{server}/watch/abc?t=120` from an email on a phone with
  the app installed: app opens to the video at 02:00.
- Tap the same URL on a phone without the app: web fallback opens in
  Safari/Chrome.
- Notification with `maktaba://job/123` deep link: app opens to the
  Queue tab scrolled to job 123.

**EC:**
- Deep link references a deleted resource (404): the app shows an
  inline "Video not found" with a "Return to library" CTA.
- Server URL has changed (user moved hosts): deep links from the old
  host fail; we surface "This link points to a different Maktaba server"
  and offer to switch the configured server.
- Malformed deep link: silently land on `/library`, log warning.

---

## Epic 13 — Desktop Apps (Tauri)

**Goal.** A Tauri 2 wrapper of the same web bundle producing native
binaries for macOS, Windows, and Linux. Native menus, file associations,
system tray, file drag-and-drop, auto-update, mDNS server discovery.

**Anchors:** architecture §6.4, §2.1 (Tauri 2).

---

### Story 13.1 — macOS app

A signed and notarized `.dmg` for Apple Silicon and Intel.

**AC:**
- Targets macOS 13+; universal binary.
- Native menu bar (Maktaba, File, Edit, View, Library, Window, Help) with
  standard shortcuts (`Cmd+Q`, `Cmd+W`, `Cmd+,` for Settings).
- Window restore: position and size persisted across launches.
- Light/dark theme follows the OS by default; Maktaba theme overrides if
  set in Settings.
- Notarized with hardened runtime; Gatekeeper accepts on first launch.
- Distributable via `.dmg` and Homebrew tap (architecture §12.4).

**TC:**
- Install the `.dmg`, drag to Applications, first launch: no security
  prompt.
- Quit and relaunch: window opens at last position with last route.
- macOS dark mode toggle: app follows.

**EC:**
- macOS 12: launch refused with a friendly "Update macOS to 13+" message.
- Notarization revoked on a stale build: user sees the system gatekeeper
  prompt; we publish a new build.
- Multiple windows open: each has independent state but shares the same
  server connection.

---

### Story 13.2 — Windows app

A signed `.msi` and `.exe` installer for x64.

**AC:**
- Targets Windows 10 1809+; ARM64 build optional v1.1.
- Native window chrome (title bar, snap zones); high-DPI aware.
- WebView2 runtime auto-installed via the bootstrapper if missing.
- Start Menu entry, file association for `.maktaba` shortcuts, taskbar
  pinning.
- Code-signed installer via EV cert; SmartScreen passes.

**TC:**
- Install on Windows 10 22H2 and Windows 11: succeeds without admin
  prompt for per-user install.
- Maximize / restore on a 4K monitor: scales correctly.
- Open a `.maktaba` shortcut from Explorer: launches app pointed at that
  server.

**EC:**
- WebView2 runtime missing and offline: bootstrapper offers an offline
  installer link.
- Antivirus quarantines the unsigned binary: we ship signed; document
  Defender false-positive process.
- ARM64 Windows running x64 emulator: works but documented as
  "experimental".

---

### Story 13.3 — Linux app

`.AppImage` and `.deb` for x86_64; `.rpm` v1.1.

**AC:**
- Built against WebKitGTK (Tauri default); compatible with Ubuntu 22.04+,
  Fedora 38+, Debian 12+.
- `.deb` installs a `.desktop` launcher and registers MIME types for
  `.maktaba` and `application/x-mpegurl` opened via the app.
- `.AppImage` is portable; runs on any compatible distro without install.
- Wayland and X11 supported; fractional scaling honored.

**TC:**
- Run `.AppImage` on Ubuntu 22.04 Wayland: window opens, mDNS discovers
  the server, video plays.
- Install `.deb` on Debian 12: launcher appears in menu, MIME registered.
- Run on a Raspberry Pi 5 (ARM64 — best-effort): document GPU video
  decode caveats.

**EC:**
- WebKitGTK missing or too old: installer surfaces apt/dnf hint with
  the package name.
- HiDPI scaling on KDE: window respects `GDK_SCALE` / Wayland scaling.
- AppArmor / SELinux blocks file dialog: documented workaround in
  release notes.

---

### Story 13.4 — System tray integration

A tray / menu-bar icon shows current activity and exposes quick actions.

**AC:**
- Tray icon present on macOS menu bar, Windows system tray, Linux
  system tray (where supported).
- Click → menu with: Now Playing, Queue (count), Recently Added,
  Settings, Quit.
- "Now Playing" is live; clicking opens the player window to the video.
- Tray icon dot/dot-with-count when jobs are running or notifications
  pending (configurable).
- Click-through closes the window without quitting (macOS / Windows);
  Quit menu item exits.

**TC:**
- Start a transcribe job in the background: tray icon shows a small
  badge with the count.
- Click "Now Playing" from tray: window comes to foreground at the
  correct route.
- Quit from tray: app exits cleanly; downloads are paused, not lost.

**EC:**
- Linux DE without tray (GNOME 40+ default): document AppIndicator
  extension or graceful degrade.
- A user disables tray entirely in Settings: window-close means quit.

---

### Story 13.5 — Local server auto-discovery (Bonjour / mDNS)

The desktop app discovers Maktaba servers on the LAN automatically and
offers them in a server picker.

**AC:**
- The desktop app advertises `_maktaba._tcp.local.` as a client and
  resolves servers advertising the same service.
- First-launch wizard: lists discovered servers with name + last-seen
  timestamp; user picks one; manual entry of `host:port` is also
  available.
- "Switch server" command in the menu re-opens the picker.
- Discovery is passive (no active scans beyond mDNS) so it consumes
  minimal bandwidth.
- Pairing across LAN uses QR code (Story 15.5) when manual auth is
  needed.

**TC:**
- LAN with one server running: the picker auto-fills it within 2 s.
- LAN with three servers: all listed; selection persists for next launch.
- LAN with zero servers: picker shows manual entry only.

**EC:**
- mDNS is blocked by VPN / corporate firewall: graceful fallback to
  manual entry; we surface "mDNS unavailable — enter your server
  manually".
- Server changes IP on the LAN: discovery re-resolves on next launch
  without user action.
- Multi-NIC machine with mDNS on every interface: dedupe by service name.

---

### Story 13.6 — File drag-and-drop to add videos

Dragging a video file into the desktop app moves / copies it into a
selected library and triggers a scan.

**AC:**
- A drop zone overlays every page when a drag enters; the drop zone
  shows "Drop here to add to {selected library}".
- Drop semantics: copy by default; Shift to move; Cmd/Ctrl to add by
  reference (no file move, just register the path).
- File type filter: only video extensions (`.mkv`, `.mp4`, `.mov`, `.avi`,
  `.webm`, `.m4v`, etc.); reject others with a toast.
- After drop, the file appears immediately in the library list with
  `DISCOVERED` state and a "Watching" badge.
- Multi-file drops are batched: a single progress toast covers the lot.

**TC:**
- Drag a 1.2 GB MKV from Finder: file copies to the library root, scan
  picks it up, transcribe job enqueues.
- Drag a 50-file batch: copy proceeds in parallel up to a cap (4 parallel);
  UI shows aggregate progress.
- Drag a non-video file: rejected with a polite toast.

**EC:**
- Source disk runs out of space mid-copy: rollback (delete partial),
  surface a clear error.
- Permissions issue (read-only library): surface "Library is read-only —
  drop disabled" before the drop completes.
- Drag from a network share: copy works but is slow; we surface the
  expected ETA.

---

### Story 13.7 — Keyboard shortcuts

Native menu items expose all shortcuts; the in-app shortcut layer
(Story 11.9) augments them.

**AC:**
- Menu items map to all Story 11.9 shortcuts plus desktop-specific:
  - `Cmd/Ctrl+N` → new window.
  - `Cmd/Ctrl+Shift+N` → new private session (no shared local cache).
  - `Cmd/Ctrl+1..9` → switch to library N.
  - `Cmd/Ctrl+,` → Settings.
  - `Cmd/Ctrl+R` → refresh current view.
  - `Cmd/Ctrl+F` → focus search.
- Native menu accelerators visible next to each item.
- Shortcuts work even when the app is not focused, for global media keys
  (Play/Pause/Next/Previous) on Windows and macOS.

**TC:**
- `Cmd+R` reloads the current route without losing player state.
- Press the keyboard Play/Pause hardware key with Maktaba in the
  background: playback toggles.
- Conflict with a global OS shortcut: the OS wins.

**EC:**
- Linux media keys vary by DE: documented per-DE behavior.
- An app with conflicting global media keys (Spotify, Apple Music): we
  yield to whichever was most recently in foreground.

---

### Story 13.8 — Auto-update

Tauri's built-in updater fetches signed delta updates from a server-side
manifest; user is prompted to install on next quit.

**AC:**
- Update channel: `stable` (default), `beta` (opt-in via Settings →
  Advanced).
- Update check on launch + every 24 h.
- Updates are signed with an Ed25519 key; the public key is bundled at
  build time.
- "Update available" toast with "Install on quit" or "Install now"
  (restarts).
- Background download with resume; once downloaded, applied at restart.
- Version skew with the server is surfaced in Settings → About.

**TC:**
- Publish a new build to the manifest: a running client picks it up
  within 24 h; user installs on quit.
- Updater fails signature check: refuses to install; logs warning.
- User on `beta` rolls back to `stable`: next available `stable` is
  installed (no downgrade unless user opts in to "Allow downgrades" in
  Advanced).

**EC:**
- Update server unreachable: silently retry next interval; do not nag.
- Disk full mid-download: pause; surface error.
- Update introduces a breaking schema change: we surface "Server is
  older than client — update server first" and refuse to migrate.

---

## Epic 14 — TV Apps

**Goal.** Native tvOS (Swift / SwiftUI / AVPlayer) and Android TV
(Kotlin / Compose for TV / ExoPlayer) apps that consume the same GraphQL
schema as every other client. 10-foot UI, D-pad navigation, voice search,
and content rows tailored for living-room viewing.

**Anchors:** architecture §6.5, §2.1 (TV stack).

---

### Story 14.1 — tvOS app (Swift / SwiftUI)

A native tvOS app targeting tvOS 17+, distributed via TestFlight then App
Store.

**AC:**
- Built with SwiftUI for tvOS using the native focus engine.
- Top Shelf integration: "Continue Watching" surfaced on the Home
  screen.
- Tabs: Home, Library, Search, Settings.
- AVPlayer for HLS playback; HDR (HLG, Dolby Vision where the device
  supports it).
- Apollo iOS GraphQL client generated from `shared/graphql/schema.graphql`.
- Apple TV Remote: focus, swipe seek, Siri Remote click, double-tap.
- Server pairing: QR code on TV → scan with phone (Story 15.5).

**TC:**
- Cold launch on Apple TV 4K: ≤ 5 s to Home with a populated Continue
  row (assuming previous activity).
- Play a 4K HDR HEVC source: direct play succeeds; HDR metadata is
  preserved.
- Voice search "lectures about gratitude" → results within 1 s.

**EC:**
- Server unreachable: Home shows the last-cached rows with a banner.
- An item in Continue row points to a deleted video: row entry hidden,
  not crashy.
- App suspended mid-playback for 30 minutes: AVPlayer resumes; if the
  manifest expired, we mint a new session and resume from
  `position_sec`.

---

### Story 14.2 — Android TV app (Kotlin / Leanback)

A native Android TV app targeting Android TV 9+ (API 28).

**AC:**
- Built with Compose for TV + Leanback row layouts.
- ExoPlayer for HLS / DASH adaptive playback; HDR10 / Dolby Vision
  where supported.
- Recommendations channel on the home screen with "Continue Watching"
  and "Recently Added".
- Apollo Kotlin GraphQL client.
- D-pad / remote / game-controller input.
- Server pairing via QR (Story 15.5).

**TC:**
- Cold launch on a Chromecast with Google TV: ≤ 6 s.
- D-pad navigation across a row of 50 items: smooth, no focus loss.
- Voice search via Google Assistant: dispatches to `/api/search/suggest`.

**EC:**
- Manufacturer skin (Sony, Sharp) with non-standard launcher: rec
  channel API has caveats; we document and gracefully degrade to
  in-app rows.
- HDR auto-engagement fails on a misconfigured TV: fall back to SDR
  with a one-time toast.
- Network drop mid-playback: ExoPlayer's default retry kicks in; we add
  a 5 s grace before showing a recoverable error.

---

### Story 14.3 — 10-foot UI design (large text, D-pad navigation)

A TV-specific layout with large type, focus rings, and predictable
D-pad geometry. Shared design tokens (Story 17.1) but TV-specific
spacing scale.

**AC:**
- Minimum body type: 28 pt at 1080p, 36 pt at 4K.
- Focus ring: 4 px outline + soft glow at the brand color; never relies
  on color alone.
- D-pad geometry: every focusable element sits on a predictable grid;
  diagonal moves not required for any flow.
- Rows use horizontal-snap focus; columns use vertical-snap.
- "Back" returns to the previous focus, not the top of the row.
- Safe-area: 5% inset on all four sides; never paint within it.
- All controls reachable with the remote alone; no swipe-only flows.

**TC:**
- Use only the Apple TV remote / Android TV remote: every flow
  completable.
- Inspect focus traversal on a row of mixed-width cards: focus moves
  predictably.
- Read body text from 3 m at 1080p: legible.

**EC:**
- A row with one item: focus left/right wraps within the row.
- Back from the player: returns to the detail page, not Home.
- A focus trap (e.g., a modal): the modal's first focusable receives
  focus; back exits the modal.

---

### Story 14.4 — Voice search integration (Siri, Google Assistant)

Voice queries dispatched to `/api/search/suggest` and `/api/search`.

**AC:**
- tvOS: `INSpeakableString` integration so "Hey Siri, search Maktaba for
  …" works while the app is foreground.
- Android TV: voice input from the system keyboard, plus an
  app-registered Assistant action `actions.intent.SEARCH`.
- Recognized utterances with no hits: surface "did you mean…" suggestions
  using `/api/search/suggest`.
- Locale-aware: voice in `ar` queries the Arabic FTS index; voice in
  `en` queries cross-language semantic.
- Spoken Arabic is normalized server-side (diacritics removed via FTS5
  `unicode61 remove_diacritics 2`).

**TC:**
- Speak "تفسير الفاتحة" on Apple TV: results within 2 s.
- Speak an English query into Android TV's mic: results render with
  Arabic snippets (cross-language).
- Speak gibberish: empty state with "did you mean" suggestions.

**EC:**
- Mic permission denied: surface the OS-level permission flow.
- Background noise causes mistranscription: show the recognized text in
  the search box so the user can correct.
- Voice provider returns nothing (rare): silently fall back to text
  search.

---

### Story 14.5 — Continue Watching row

The Home screen's first row, populated from `playback_state` with
in-progress videos sorted by most recently played.

**AC:**
- Row title: "Continue Watching" (or localized).
- Items: poster + title + remaining time + progress bar overlay.
- Min progress to qualify: 5%; max progress: 95% (above that the video
  is "Watched").
- Cross-device: started on phone shows up on TV within 5 s of the last
  position update.
- Long-press / context menu: "Mark as Watched", "Remove from Continue".
- Empty state: "Nothing in progress yet — start a video on any device".

**TC:**
- Watch 12 minutes of a 1-hour video on the phone: the row updates on
  the TV within 5 s.
- Mark watched on phone: row entry disappears on TV.
- Delete the underlying video: row entry hidden.

**EC:**
- A video with `duration_sec = 0` (probe pending): excluded from the
  row.
- A user with > 50 in-progress videos: row caps at 20, sorted by
  recency.
- Duplicate entries (same video in two collections): single entry only.

---

### Story 14.6 — Recommendations based on viewing history

A row "Because you watched X" populated from semantic-similarity over
the user's recently watched titles.

**AC:**
- Source: server-side endpoint `GET /api/recommendations` returning a
  list of `{title, items[], reason}` rows.
- Reason: "Because you watched X", "More like Y", "Speakers you follow",
  "Newly added in your favorite library".
- Up to 5 rows; each up to 20 items.
- Row composition is deterministic per user per day (cached server-side
  for 24 h, recomputed nightly).
- "Not interested" affordance hides items / reasons; persisted per user.

**TC:**
- Watch three sermons by the same speaker: a "More from {speaker}" row
  appears within 24 h.
- Hide a row: remains hidden on next launch.
- New user with no history: rec rows show "Newly added" and "Editor's
  picks" only.

**EC:**
- All recommendations would have ≤ 1 item: row hidden rather than
  half-empty.
- A "Speakers you follow" row when no speaker is followed: silently
  omitted.
- Cold-start (no watch history): no personalized rows; only newly added
  and editor's picks.

---

## Epic 15 — Discovery & Networking

**Goal.** Make Maktaba easy to find on the LAN, optionally reachable
from the open internet, and pair-able across devices in seconds.
mDNS / Bonjour for LAN, an opt-in cloud relay for remote access,
optional federation between instances, UPnP/DLNA for legacy clients,
and QR-based pairing.

**Anchors:** architecture §6 (clients), §9.4 (Streaming auth).

---

### Story 15.1 — Local network discovery (mDNS / Bonjour)

The Maktaba server advertises itself on the LAN; every client discovers
it without manual entry.

**AC:**
- Server advertises `_maktaba._tcp.local.` with TXT records:
  `version=`, `name=`, `tls=`, `auth_required=`, `mdns_id=` (a
  per-server stable UUID).
- Client (web is exempt; mobile and desktop included) queries on launch
  and on network-change events.
- Web client cannot browse mDNS directly; it relies on a captive-portal
  style "Open Maktaba" link in the discovery agent app, or manual URL.
- Server registers under both `local.` and any configured search domains.

**TC:**
- Server on LAN, mobile app cold-launch: discovered within 2 s; no
  manual entry needed.
- Server restart: TXT records re-published; clients re-resolve within
  10 s.
- Two servers on the same LAN: client picker (Story 13.5) shows both.

**EC:**
- LAN with mDNS reflectors / VLAN segmentation: server advertises on the
  bound NIC only; document multi-VLAN setups.
- Server changes hostname: clients see two entries until the old one TTLs
  out; we treat `mdns_id` as the canonical identity.
- IPv6-only LAN: mDNS works over LL-multicast; `AAAA` records published.

---

### Story 15.2 — Global discovery (optional cloud relay)

For users who want remote access without opening ports, an opt-in cloud
relay tunnels traffic to the home server.

**AC:**
- Relay protocol: outbound long-lived QUIC connection from server to
  relay; clients connect to relay and are routed.
- Strictly opt-in; off by default. Settings → Remote Access toggles it.
- Relay is end-to-end encrypted: the server holds the TLS cert,
  relay sees only ciphertext.
- Relay user identity is bound to the Maktaba account; no separate
  login.
- Quota: free tier 50 GB/month, premium tier higher (Story 16.x).
- Latency overhead documented; "Direct" / "Relayed" badge in the
  client's connection status.

**TC:**
- Enable remote access on the server, open the mobile app on cellular:
  app reaches the server via the relay; video plays.
- Toggle off: subsequent connection attempts on cellular fail with
  "Server unreachable — enable remote access?".
- Quota exhausted: reads continue but new sessions block until next
  cycle, with a clear error.

**EC:**
- Relay node failover: server reconnects to a healthy node within 30 s;
  client sessions continue.
- Server's outbound is firewalled: relay connection fails; UI surfaces
  the diagnostic.
- Relay outage: clients fall back to LAN-only.
- Some jurisdictions require data residency: relays in `eu`, `us`,
  `ap` regions; user picks at opt-in time.

---

### Story 15.3 — Server-to-server federation (optional)

Two Maktaba instances can opt to share libraries with each other. A
federated library appears as a second row in the picker and is browsable
read-only by default.

**AC:**
- Pairing: admin generates a federation token; remote server enters the
  token; both instances exchange public keys + a signed agreement.
- Federation is asymmetric: A → B can read A's `Lectures`, B → A can
  read B's `Films`; permissions per-library.
- Federated browsing uses the same GraphQL schema, with a
  `federationOrigin` field on every `Video`.
- Federated streaming: bytes flow directly from the owning server's
  Streaming Service to the consuming client (the client holds two JWTs).
- Federation is off by default and never enabled silently.

**TC:**
- Pair instance A and B; B sees A's `Lectures` library read-only.
- Play a video from a federated library on B: stream comes from A.
- Revoke federation: B no longer sees A's library; in-flight sessions
  expire on next manifest refresh.

**EC:**
- A is offline: federated library on B shows a banner "Source server
  offline".
- A renames a library: B's reference is broken; surface an admin warning.
- Conflict resolution: same `content_hash` on both A and B → B prefers
  its local copy unless the user explicitly browses A's library.
- Federation token leaked: revocation is immediate via
  `DELETE /api/federation/{partner_id}`.

---

### Story 15.4 — UPnP / DLNA compatibility

For legacy devices (older smart TVs, PS3-era consoles), Maktaba speaks
basic DLNA so the library is browsable as a media server.

**AC:**
- Opt-in toggle in Settings → Compatibility.
- Advertises as a `MediaServer` per UPnP AV; transcoded HLS sources are
  not exposed (DLNA can't consume them); only direct-play files are.
- DLNA clients see a flat list (no tagging / search / progress sync).
- Read-only: no DLNA-side delete or upload.
- Browsing tree: Library / Genre / Speaker / Recently Added.
- Subtitles: sidecar SRT exposed where the DLNA client supports them.

**TC:**
- Enable DLNA, browse from a Sony Bravia (2018): library appears and
  plays.
- Browse from VLC (DLNA client): same.
- Disable DLNA: server stops advertising within 30 s.

**EC:**
- DLNA-incompatible codec (HEVC on a 2014 TV): we don't advertise that
  file (would fail on the TV anyway).
- Cellular network mistakenly being treated as LAN by UPnP IGD: we
  refuse to advertise on non-private addresses.
- DLNA UUID conflicts with another product on the LAN: pick a
  deterministic UUID derived from `mdns_id`.

---

### Story 15.5 — QR code pairing for mobile → server

A pairing flow that lets the user point a phone at a TV / desktop's QR
code and bind the mobile app to the same server with the same login.

**AC:**
- The TV / desktop generates a one-time pairing code:
  `POST /api/auth/pair {device_name, device_type}` returns a 6-digit
  human code + a QR-encoded URL.
- The QR URL has form `https://{server}/pair?code=ABC123` and embeds the
  server's mDNS ID + LAN address.
- The mobile app's "Add device" flow scans the QR; if the encoded server
  is reachable on LAN it pairs directly, else it falls back to the relay.
- Pairing exchanges a refresh token tied to the device; valid for 30 d.
- Pairing code TTL: 5 min; one-time use; expires immediately on
  successful pair.

**TC:**
- TV shows QR; phone scans; phone is logged in within 3 s.
- Re-scan an expired QR: surfaces "Pairing code expired — generate a
  new one on TV".
- Pair across cellular (relay path): same flow, slower.

**EC:**
- QR contains a server the phone has never seen: surface a confirmation
  "Pair with `maktaba.local`?" before committing.
- Camera permission denied: fall back to manual code entry (6 digits).
- Phishing-style fake QR: pairing checks the server's TLS cert and
  refuses if it's not the same `mdns_id` as a known server, unless the
  user explicitly chose "Add new server".

---

## Epic 16 — Subscriptions & Monetization (Optional)

**Goal.** Maktaba is fully usable for free as a self-hosted single-user
product. Optional premium features (cloud relay quota, multi-user
seats, cloud metadata backup, advanced analytics) are gated by a
license key and validated against a license server. All paid features
are server-side gates; the client surface only enables UI.

**Anchors:** architecture §10.4 (cost control), §11.5 (secrets).

---

### Story 16.1 — Free tier (local only, single user)

The free tier is the canonical product: full library, full streaming,
full search, full clients, single user. No nag screens, no expiring
features.

**AC:**
- All Epics 1–15 features work without a license key.
- "Get Premium" entry point exists in Settings but is unobtrusive (no
  modal nags).
- Single-user mode: bootstrap admin token (architecture §9.8); no user
  table required.
- LAN-only: cloud relay disabled; multi-user disabled; cloud metadata
  backup disabled.
- License-server unavailable: free tier is unaffected.

**TC:**
- Fresh install with no license key: every documented feature works.
- Disconnect the license server: free tier features remain working.
- Open a UI element gated to premium: it's hidden, not just disabled.

**EC:**
- A user accidentally entered then removed a premium key: free tier
  resumes; no data loss.
- Migrating from premium back to free: any premium-only data (analytics
  history beyond 30 d) is preserved server-side but read-only.

---

### Story 16.2 — Premium features (remote access, multi-user, cloud backup)

Premium adds remote-access relay quota, multi-user seats, scheduled
metadata backup, and advanced analytics dashboards.

**AC:**
- Feature flags: `relay`, `multi_user`, `backup`, `analytics`,
  `federation`. Each gated by the license tier (`free`, `home`, `pro`).
- `home` tier: relay 200 GB / mo, 4 user seats, daily backup, basic
  analytics.
- `pro` tier: relay 1 TB / mo, unlimited seats, hourly backup, advanced
  analytics, federation.
- Server enforces gates; clients only render the UI conditionally.
- Downgrading: features remain visible read-only for 30 d, then hidden.

**TC:**
- Apply a `home` license: 4 seats become creatable; the 5th refuses with
  a clear quota message.
- Backup runs on schedule and produces a `.maktaba-backup` archive in
  the configured destination.
- Downgrade `pro` → `home`: federation pairings remain visible but new
  ones blocked.

**EC:**
- License clock skew (server time vs. license expiry): grace period of
  72 h before features lock.
- License revoked due to fraud: server receives revocation list; locks
  immediately with a clear admin message.
- A user is mid-backup when license expires: that backup completes; the
  next is blocked.

---

### Story 16.3 — Subscription management

The user can manage their subscription from Settings → Subscription:
view tier, expiry, payment method, change tier, cancel.

**AC:**
- Read-only by default; "Manage" deep-links to the billing portal
  (Stripe Customer Portal or equivalent).
- Cancellation flow: confirm modal with "downgrade takes effect on next
  renewal" copy.
- Upgrades are immediate; downgrades take effect at renewal.
- Receipts: list of past invoices with downloadable PDF (Stripe-issued).
- VAT / tax indication where applicable.

**TC:**
- Upgrade home → pro: features unlock within 60 s of webhook arrival.
- Cancel: the UI shows "Cancels on 2026-06-01"; daily reminder is off
  by default.
- Restore a cancelled subscription before expiry: feature parity preserved.

**EC:**
- Webhook delivery failure: we retry with backoff; reconcile via daily
  cron against Stripe's source of truth.
- A user double-purchases on two devices: server dedupes by Stripe
  customer ID; the second purchase is refunded automatically.
- Disputed payment: license tier flips to `free` until resolved; no
  data is destroyed.

---

### Story 16.4 — License key validation

Offline-tolerant license validation: keys are public-key signed; the
server checks the signature locally and refreshes against the license
server periodically.

**AC:**
- License keys are Ed25519-signed JSON: `{license_id, tier, seats,
  expires_at, signature}`.
- Server bundles the license-server public key at build time.
- Validation: signature check + expiry check + seat-count check.
- Daily refresh against the license server; 30 d offline grace before
  features lock.
- Revocation list fetched daily; if a license id is on the list, lock
  features immediately with admin notification.
- License keys are never logged or returned by `/api/settings`; admin
  can paste in but the field is write-only after submission.

**TC:**
- Apply a valid signed license: server unlocks features within 5 s.
- Tamper with the license JSON: signature fails; features stay free.
- Disconnect from the license server for 35 d: features lock with a
  clear "Reconnect to validate license" admin banner.

**EC:**
- Clock manipulation (user sets system clock back): we trust the
  license server's `expires_at` over local time when reachable.
- License covers seats=4 but server has 5 users: existing 5 keep working
  read-only; new logins refused. Admin warned.
- Revocation reaches the server while offline: we use the last-known
  list; on reconnect we re-evaluate.

---

### Story 16.5 — Usage analytics (opt-in)

Anonymous usage analytics, strictly opt-in, with a clear scope and a
visible kill switch.

**AC:**
- First-launch: opt-in dialog "Help improve Maktaba" with bullet list of
  what's collected; no telemetry until accepted.
- What's collected: app version, OS, anonymized library size, feature
  usage counts, error stack traces (no file paths or content).
- What's never collected: video filenames, transcript text, search
  queries, user identifiers, IP addresses (after sampling).
- Aggregated server-side; per-user data deletable via "Forget my
  device" button.
- Endpoint: `POST /api/telemetry` to a separate Maktaba telemetry host;
  user can self-host it.
- Self-host server-side opt-out: `[telemetry] enabled = false`.

**TC:**
- Opt in: the next session's events appear on the telemetry server
  within minutes.
- Toggle off: events stop firing within one app launch.
- "Forget my device": the device's pseudonymous ID and history are
  purged.

**EC:**
- Network drops while sending events: queued locally; capped at 1,000
  events; oldest dropped first.
- Telemetry endpoint returns 5xx: client retries with exponential
  backoff; never blocks UI.
- A locale that requires explicit consent (EU): treat the opt-in dialog
  as the consent record.

---

### Story 16.6 — Feature flags per tier

A feature-flag layer that enables / disables UI elements based on the
license tier and per-user roles.

**AC:**
- Server returns `GET /api/me/flags` with the resolved flag set for the
  current user (tier, role, beta-cohorts).
- Client respects flags: gated UI is hidden, not disabled with a
  paywall, except for clearly opt-in upgrade affordances.
- Flags are cached for 60 s; refreshed on app foreground.
- Beta flags: opt-in via Settings → Advanced → Experiments; documented
  as unstable.

**TC:**
- Tier flips premium → free mid-session: gated UI disappears on next
  flag refresh.
- A beta flag is rolled back server-side: client picks it up within 60 s.
- A user with role "admin" sees admin-only flags; a regular user does
  not.

**EC:**
- Flag refresh fails: use the cached set; never enable a flag that
  failed validation.
- Conflicting flags (`relay = true`, `quota = 0`): UI shows feature but
  uses 0 quota; server rejects use.
- Tampering with the local cache: flags are signed by the server and
  re-checked on every privileged action.

---

## Epic 17 — UX Design System

**Goal.** A coherent visual + interaction language across web, mobile,
desktop, and TV. Design tokens are the source of truth; components,
motion, copy, and screens reference them. RTL is first-class; the
system "doesn't have an Arabic mode", it has an Arabic baseline that
LTR adapts to where required.

**Anchors:** architecture §6 (clients), §11 (config-driven theming).

---

### Story 17.1 — Design tokens (colors, typography, spacing)

A token registry exported as CSS custom properties (web), JSON (Capacitor
plugin), Swift `struct` (tvOS), and Kotlin `object` (Android TV).

**AC:**
- Token domains: color, typography, spacing, radius, elevation, motion,
  z-index, breakpoints.
- Color: brand palette + semantic tokens (`--color-bg`, `--color-fg`,
  `--color-accent`, `--color-success`, `--color-warn`, `--color-error`).
  Light + dark variants.
- Typography: 4 type roles (display, body, mono, transcript). Arabic
  font (`IBM Plex Sans Arabic`), Latin font (`Inter`); fallback stack
  documented.
- Spacing: 4 px base unit; 4, 8, 12, 16, 24, 32, 48, 64, 96 px scale.
- Single source of truth: `design/tokens/tokens.json`; build pipeline
  generates the four target outputs.
- Versioned: bumping a token bumps the design-system semver; clients
  pin a major version.

**TC:**
- Change a brand color in `tokens.json`: web, iOS, Android, tvOS, AndroidTV
  all rebuild with the new color.
- Switch theme dark → light: every token resolves correctly per token
  set.
- Generate the Swift output: compiles with the tvOS target.

**EC:**
- A native target requests a token that doesn't exist: the build fails
  loud, never falls back to a hard-coded default.
- A user's high-contrast OS mode: token set is overridden by a separate
  `tokens.high-contrast.json`.
- Token rename mid-version: shipped as a deprecated alias for one major.

---

### Story 17.2 — Component library (buttons, cards, modals, forms)

A React component library used by the web, Capacitor, and Tauri shells;
mirrored in SwiftUI and Compose for TV.

**AC:**
- Components: Button (5 variants, 3 sizes), IconButton, Link, Input,
  Textarea, Select, Combobox, Toggle, Checkbox, Radio, Card, Modal,
  Drawer, Toast, Tooltip, Tabs, Pagination, ProgressBar, Skeleton,
  EmptyState, ErrorState, Avatar, Badge, Chip, Menu, ContextMenu.
- Every component documented in Storybook with controls and a11y notes.
- Every component accepts a className escape hatch but defaults to
  tokens.
- Forms: a `<Form>` wrapper with `react-hook-form` + zod validation;
  errors localized via i18n.
- Native counterparts (TV): equivalent SwiftUI / Compose components,
  hand-maintained but covered by visual-regression snapshots against
  Storybook.

**TC:**
- Render Storybook in CI: visual diffs gate merges.
- A button with a loading state shows a spinner and is unfocusable.
- A modal traps focus and closes on `Esc`.
- TV variant of `Card` receives focus and grows by 4% under D-pad
  navigation.

**EC:**
- A child element overflows a card: visible scrollbar, never clipped
  silently.
- A component used outside a `<ThemeProvider>`: warns in dev, falls
  back to defaults in prod.
- Heavy component (DataTable) is lazy-loaded; falls back to a skeleton
  while loading.

---

### Story 17.3 — Motion / animation guidelines

A documented motion system: durations, easings, and patterns for
common transitions.

**AC:**
- Durations: 100 ms (instant), 150 ms (quick), 250 ms (standard),
  400 ms (relaxed), 600 ms (theatrical, sparingly).
- Easings: `easeOut` for enter, `easeIn` for exit, `easeInOut` for
  reposition.
- Patterns: page transition (200 ms cross-fade), modal (250 ms scale +
  fade), toast (slide-up + fade), focus ring (instant).
- All motion respects `prefers-reduced-motion`.
- No spring physics for layout (causes nausea on TV); allowed for
  player chrome.

**TC:**
- Toggle reduced motion: every animated element falls to a 0 ms
  transition.
- Open a modal with `useReducedMotion`: the scale animation is skipped;
  fade remains.
- Player chrome reveal/hide: 150 ms; never blocks input.

**EC:**
- A device with `prefers-reduced-motion: reduce` and an essential
  animation (loading spinner): the spinner becomes a static "Loading…"
  text + dot animation kept under 1 Hz.
- 60 fps not achievable on a low-end Android: motion durations clamp to
  150 ms regardless of token.
- Conflict between two simultaneous animations on the same element: the
  later wins; we never blend.

---

### Story 17.4 — Loading states and skeleton screens

Every async surface has a defined loading state: skeleton for content,
spinner for actions, indeterminate progress for unknowns.

**AC:**
- Skeleton: shape-matches the final content; never shows for < 200 ms
  (avoid flash); maxes at 5 s before swapping to a generic spinner +
  retry.
- Spinner: only for action-bound waits (button submitting, modal
  saving); never for page-level loads.
- Empty placeholder during pagination: 6 skeleton rows.
- Search dropdown: shimmer while suggestions load.
- Player initial buffer: a centered spinner over the poster, ≤ 2 s.

**TC:**
- Slow-network test (200 ms latency): skeletons appear; do not flash
  for < 200 ms.
- Skeleton-to-content swap is layout-stable (no CLS).
- Hold a button: spinner replaces the label; button width preserved.

**EC:**
- A 0-ms response: skeleton never shown (under the 200 ms minimum).
- A 30 s load: skeleton timeout → "Still loading" text → retry CTA at
  60 s.
- Player buffer underrun mid-playback: spinner over the player center
  with a "Buffering…" caption.

---

### Story 17.5 — Error states and empty states

Every screen has a documented error and empty state with copy that
explains and a primary recovery action.

**AC:**
- Error states classified: `network`, `server`, `permission`,
  `not_found`, `validation`. Each has a token-driven illustration and
  copy template.
- Empty states classified: `first_run`, `filtered_out`, `cleared`. Each
  has a primary CTA.
- Copy follows a tone guideline (Story 17.x of UX Copy): clear,
  direct, no blame.
- Error toasts: 4 s default; sticky for `permission` and `not_found`.
- Retry actions are idempotent and de-duped.

**TC:**
- Disconnect network and load the library: a network error illustration
  + "Try again" button appears.
- Filter library to nothing: empty illustration + "Clear filters".
- A 404 on a deep-linked video: "Video not found" + "Return to library".

**EC:**
- An error during an error (retry fails the same way): single
  consolidated message; no error storm.
- A permission error caused by a missing library scope: surface "Ask
  your admin to grant access".
- A user dismisses an error then re-triggers it within 5 s: show only
  once, dedupe.

---

### Story 17.6 — Onboarding flow (first-time setup wizard)

A 4-step setup wizard for first launch: choose admin password, add a
library, configure STT, pick UI language and theme.

**AC:**
- Step 1: server-name + admin password (or skip in single-user mode
  with bootstrap token).
- Step 2: pick a library root (browse FS or paste path); show estimated
  size.
- Step 3: pick STT backend (auto-detected: MLX on Apple Silicon, CUDA on
  NVIDIA GPU, CPU fallback); show the trade-off matrix.
- Step 4: language (Arabic / English) + theme (light / dark / system).
- "Skip" available on every step except Step 1; defaults are sane.
- Progress bar at the top; back-arrow on every non-first step.
- On completion: a one-time "Tour the app" carousel (Library, Search,
  Queue, Player) — dismissable.

**TC:**
- Single-user mode: wizard skips Step 1; lands on Step 2.
- Choose `whisper-cpu`: Step 3 warns about realtime factor.
- Cancel mid-wizard: the user lands on a "Resume setup" banner on next
  launch.
- Tour carousel: dismissable from any panel; never shown again
  unless the user clicks "Show me again" in Settings → About.

**EC:**
- Disk has no writable library root: surface "Create a folder for me?"
  CTA that creates `$HOME/Maktaba/Library`.
- STT backend auto-detect fails (no GPU, no MLX): default to
  `whisper-cpu` with a "this will be slow on a CPU" warning.
- Onboarding interrupted by an OS update / reboot: state persisted
  server-side; resume is exact.

---

### Story 17.7 — Arabic RTL layout system

RTL layout is a baseline, not an after-the-fact mode. Components are
authored direction-agnostic.

**AC:**
- Logical CSS only: `padding-inline-start`, not `padding-left`.
  `margin-inline-end`, not `margin-right`.
- Icons: every directional icon (chevron, arrow, play) has an
  RTL-flipped variant; a `<DirectionalIcon>` chooses correctly.
- Numbers: configurable via Settings → Advanced (Arabic-Indic vs.
  Western numerals); times always Western for consistency in scrubbers.
- Mixed-direction text: bidi isolates (`unicode-bidi: isolate`)
  required on every span that may contain the opposite script.
- RTL visual regression: every Storybook story has both LTR and RTL
  snapshots.

**TC:**
- Switch UI to Arabic: every screen flips correctly; no orphaned
  physical-direction CSS.
- Mixed transcript snippet (Arabic + Latin name): names render LTR
  inside an RTL container.
- Player controls in RTL: skip-back is on the right of skip-forward
  (logically next/previous, not physically).

**EC:**
- A third-party component without RTL support: wrap with `dir="ltr"`
  and document deviation; do not silently break.
- Arabic font fails to load: fall back to system Arabic (Helvetica
  Arabic, Geeza Pro); never to a Latin font that renders Arabic as
  boxes.
- Numerals localization disabled mid-session: re-render number formats.

---

### Story 17.8 — Video player controls design

A minimal, focus-aware control bar with play/pause, scrubber, time,
captions, audio, settings, fullscreen, PiP, AirPlay/Cast.

**AC:**
- Auto-hide after 3 s of inactivity; reveal on mouse move, tap, or
  remote D-pad.
- Scrubber shows chapter ticks, sprite preview on hover, current
  position, buffered range.
- Captions button: cycles through tracks; off-state badge shows
  current language.
- Settings menu: speed, quality, audio track, subtitle track,
  subtitle styling.
- Touch targets: 44 × 44 CSS px on touch.
- TV variant: same controls, larger spacing, focus-ring driven.
- Subtitle style controls: size, color, background, font (sans / serif),
  position (bottom / top); persisted per user.
- Mini-player: appears when navigating away from `/watch/{id}`,
  pinnable, dismissable.

**TC:**
- Hover scrubber on web: sprite preview appears within 200 ms.
- D-pad on TV: focus moves predictably across controls.
- Subtitle styling change: live-applied without restarting the video.

**EC:**
- A video with no chapters: ticks hidden.
- Sprite cache miss: scrubber preview shows the poster instead.
- Caption track upload mid-watch: button updates to include the new
  track; doesn't pause playback.

---

### Story 17.9 — Search results presentation

Search results are scannable, with hits grouped by video, snippets
truncated, timestamps clickable, facet counts visible.

**AC:**
- Result group: poster, title, language flag, duration; right side: hit
  count + first 3 snippets.
- Snippet: ≤ 160 chars; `<mark>` highlights the query; ellipsis
  on either side.
- Timestamp chip: `[06:12]` clickable, opens player at that second.
- Facet sidebar: language, library, speaker, content type, date,
  duration. Collapsible.
- "Why this result" disclosure: shows BM25 score and semantic similarity
  (admin only by default).
- Sort: Best match (default), Most recent, Most matches.

**TC:**
- A query with 1,200 hits across 80 videos: render the first 20 video
  groups; pagination for the rest.
- Click a timestamp: the player opens at the exact second.
- Facet count drops to 0: facet entry hidden, not greyed.

**EC:**
- A query that hits a video deleted between search and click: surface
  "Video no longer available" inline.
- Snippet contains an unbalanced bidi span: bidi isolate prevents UI
  bleed.
- A speaker facet with 1 hit: sorted to the bottom.

---

### Story 17.10 — Processing progress visualization

The Queue dashboard's per-job visualization is consistent across web,
desktop, mobile, and TV: a horizontal bar with audio-time annotation,
ETA, state, and stage.

**AC:**
- Bar segments: `done` (filled) | `current` (animated stripe) |
  `pending` (empty); color-coded by state.
- Annotation: `01:23:17 / 04:12:04 (33%)` — audio-time, not
  wall-clock.
- ETA next to the bar; updated only after 3 segments have committed
  (Story 11.5 EC).
- Stage indicator: small icon strip showing `scan → probe → extract →
  transcribe → index → thumbnail` with the current stage highlighted.
- Pause / resume / cancel inline buttons; Force-Pause appears after
  `pause_grace_sec`.
- Tooltip on hover: backend, model, attempts, last heartbeat.

**TC:**
- A running job updates the bar 1 Hz; reduced motion clamps to 0.5 Hz.
- A paused job's bar shows the resume offset as a vertical line.
- A failed job's bar shows the failure point and an error icon.

**EC:**
- A job with `total_duration_seconds = NULL` (probe pending): bar shows
  indeterminate stripes.
- A job that resumes from a different model: stage strip shows a
  "model upgraded" hint.
- A job whose duration metadata changed (file replaced) mid-flight:
  surface a "duration changed during processing" warning; bar uses the
  new duration as the denominator for progress that's after the swap.

---

### Story 17.11 — Subtitle and transcript presentation

The transcript sidebar and inline subtitle layer share styles and
behaviors so the user's eye doesn't have to re-learn either.

**AC:**
- Transcript sidebar: list of segments with `[mm:ss]` prefix, speaker
  badge if diarized, current segment highlighted in real time.
- Click a segment: player seeks to its `start_sec`.
- Search inside transcript: `Cmd/Ctrl+F` opens an inline find bar.
- Inline subtitles: same font + size as transcript, with stronger
  background contrast.
- Auto-scroll the sidebar to keep the current segment in view; toggle
  off ("Free scroll") in transcript settings.
- "Copy transcript" / "Copy timestamped transcript" actions.

**TC:**
- Play a video; sidebar's current segment highlight tracks the player
  within 200 ms.
- Click a segment: player seeks; sidebar centers on the clicked item.
- Search transcript for a phrase: matches highlighted; `n` / `Shift+n`
  cycles through them.

**EC:**
- A segment with no text (silence): rendered with a dim placeholder;
  not skipped (preserves timing context).
- A segment with bidi-mixed text: bidi isolate so neither direction
  bleeds.
- 50,000 segments: virtualized; auto-scroll seeks via virtual list
  index, not pixel offset.

---

## Cross-cutting checklist

This section is intentionally short. The full quality bar lives in the
individual stories above; this is a one-page sanity sweep before any
client-surface PR ships.

- **API contract:** every story consumes only documented endpoints from
  architecture §9. New endpoints require a §9 amendment.
- **GraphQL types:** every TypeScript / Swift / Kotlin client uses
  generated types from `shared/graphql/schema.graphql`. No hand-rolled
  types.
- **WebSocket fan-in:** subscriptions live in
  `internal/ws` (Go) and `lib/ws.ts` (web); not duplicated per page.
- **i18n:** no string in JSX. No string in Swift `Text(...)`. No string
  in Kotlin `Text(...)`. All through the i18n table.
- **Tokens:** no hard-coded color, font, or spacing. Always a token.
- **A11y:** every interactive control keyboard-reachable; every page
  passes axe-core; every motion respects reduced-motion.
- **Telemetry:** strictly opt-in; never shipping content text or
  filenames.
- **Tests:** every story has unit tests for logic, integration tests
  for API contract, and at least one e2e (Playwright on web; XCUITest
  on tvOS; Espresso on Android TV; Detox on Capacitor) for the happy
  path.
- **Performance budgets:**
  - Web first contentful paint ≤ 1.5 s on a 50 Mbps LAN, cold cache.
  - Search round-trip ≤ 500 ms p50, ≤ 2 s p99 on a 15,000-hour index.
  - Player join time ≤ 2 s for direct play; ≤ 4 s for transcoded.
  - PWA total bundle ≤ 350 KB gzipped for the app shell.
- **Security:** no secret in `localStorage`; refresh tokens in
  Keychain/Keystore (mobile/desktop), httpOnly cookies (web).

---

## Dependencies

Inter-epic dependencies that affect sequencing:

- Epic 11 (Web UI) is a prerequisite for Epic 12 (mobile) and Epic 13
  (desktop): both wrap the web bundle.
- Epic 17 (Design System) blocks all UI epics for tokens and components;
  start Story 17.1 + 17.2 first.
- Epic 14 (TV) depends on Epic 17 for design parity but does not
  consume the React bundle.
- Epic 15 (Discovery) is largely orthogonal; mDNS (15.1) blocks
  Story 13.5 (server picker).
- Epic 16 (Subscriptions) is orthogonal but gates Stories 15.2 (relay)
  and 15.3 (federation) at the `pro` tier.

## Out of scope (these epics)

- Live ingestion (architecture Appendix B).
- DRM-protected content.
- Multi-tenant SaaS hosting (a separate product).
- Cast Receiver app (Maktaba is not a Cast endpoint).
- On-the-fly translation between languages.

## Open questions

1. **Mini-player across routes.** Persist the Vidstack player instance
   across React Router transitions, or always re-create with a saved
   position? Decision affects Story 17.8.
2. **Federated search.** Stories 15.3 covers federated browse only;
   federated search (one query → many servers) is a separate story
   pending a §9 search-fan-in amendment.
3. **TV app feature parity.** Settings parity on TV is unrealistic;
   how much do we trim to keep the 10-foot UI usable? Stories 14.1 and
   14.2 currently assume "Settings is web/mobile/desktop only".
4. **Subscription pricing.** Stories 16.x are tier-shaped but
   intentionally avoid prices; a product decision is required before
   the billing portal goes live.
5. **Cloud relay vendor.** Self-hosted (Cloudflare Tunnel-style),
   first-party, or hybrid? Affects Story 15.2 SLA and Story 16.2 quotas.
