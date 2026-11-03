# Story 51.10 — The Components page

> Epic 51 · Phase 2 · Depends on: 51.08 · Needed by: 51.17

## Why

The owner asked for parts of Maktaba to be "stopped/started, enabled/disabled" from settings.
The admin API exists after 51.08, but owners use the web. Today the nearest tools are spread
out: stage pauses on the Queues page, extensions on their own page, `[llm]` and `[tv]` only in
`server.toml`. This story adds one page that shows every part of the home, what it is doing and
why, and lets an admin turn it on, off, hold it, and see the effect before anything changes.

## Description

### 1. Where it lives

`/settings/components`, a new tab in Settings (`web/src/pages/Settings.tsx`), shown to admins
only, next to Models and Rules. A viewer who types the URL gets the same "admins only" panel as
the other server tabs. The API refuses them anyway (51.08). The Desktop app's tray and window
menu get an **Open Components** item beside **Open logs** (`apps/desktop/src-tauri/src/tray.rs`
`WINDOW_ITEMS`). It opens the page on the local home in the browser with `agent::open_path`,
the way the update log links open `<web_url>/admin/logs` (`attempts.rs`).

### 2. Layout

```
Components                                           Based on Full, minus YouTube   [?]
───────────────────────────────────────────────────────────────────────────────────────
Processing
  Transcription           ● Running · 2 machines          [On ◉]  [Stop ▾]  Settings  ⋯
  Subtitles               ● Running                        [On ◉]  [Stop ▾]  Settings
  Semantic search         ○ Waiting · needs ML runtime     [On ◉]                     ⋯
Live TV
  Live TV                 ● Running                        [On ◉]  [Stop ▾]  Settings
  TV slots                ● Running                        [On ◉]  [Restart]
  HDHomeRun               ○ Off                            [Off ◯]
Models                    (install and remove come with 51.16)
  Whisper turbo           ● Installed · 1.6 GB
Always on
  Database, API, Web, Streaming, Pipeline   ● Running   (no switch)
```

- **Groups** come from the catalog's `group` (51.01): media, library, processing, livetv,
  integrations, cluster, system, runtimes, models, tools, languages, and `core` last as
  "Always on". Titles from `components.group.<id>` (new words, en + ar).
- **Phase chip** per row: the phase name (§4.3) and a colour. Running and Installed green,
  Degraded and Waiting amber, Failed red, Off and Stopped grey, Starting / Draining / Installing
  with a spinner and a percentage when known. The chip's second part is the reason in words
  (`components.reason.<code>`), never the raw code. A phase that differs per machine shows
  "2 of 3 machines" with a link to §5.
- **Enable switch** writes `want = enabled` / `installed`. `core.*` rows have no switch.
- **Stop ▾** opens three choices: *Until I start it*, *For 1 hour*, *Until tomorrow 06:00* (home's
  local time; the API reports the home's time zone with the list, see Edge cases). A held
  row shows **Start** and "Stopped until 06:00" instead.
- **Restart** for `process` and `loop` kinds only.
- **Settings** links to the component's form: a `rules.*` section opens
  `/settings/rules#<section>`; a `server.toml` key shows the key and says
  "server.toml says otherwise; the database wins" when they differ (§4.7).
- **⋯** opens a side drawer: conditions per machine, requirements and dependents, version,
  uptime, restarts, memory and disk, the last 20 changes from the audit log, and the
  install size.
- **Install size** shows on asset rows and in the drawer ("1.6 GB on disk"). The download size
  of something not installed shows from 51.16.

### 3. The confirm dialog

Every change first calls `POST /api/components/preview` (51.08). If the change touches only
the row itself and parks nothing, it applies at once with an undo toast (5 s). Otherwise a
dialog shows the effect in plain words and waits:

> **Turn off LLM?**
> This also stops: Smart grouping, Book detection.
> 1,840 queued jobs will wait until you turn it back on. Nothing is lost.
> Jobs running now get up to 30 seconds to finish.
> [Cancel] [Turn off]

- Conflicts: "Turning on In-process TV turns off TV slots." The confirm sends
  `with_conflicts: true`.
- Refusals show the reason and the blocking components as links, with no confirm button:
  "Remove the ML runtime? Transcription and Subtitles need it. Turn them off first."
- Every write sends the `generation` it read. On `409 stale_generation` the page reloads
  the row and says "Someone else changed this. Here is what it is now."

### 4. Live updates

The page polls `GET /api/components` every 5 s while open and visible, and at once after a
write, so phases move from Draining to Off while the admin watches. It stops polling when the
tab is hidden. Same pattern as the Machines page.

### 5. Per machine

When the home has cluster machines (Epic 31), each row with a per-machine difference has a
**Machines** link to `/admin/machines#components-<machine>`. The per-machine switches
themselves are 51.12; this page shows the home-wide wish and the per-machine status.

### 6. Words

Every string ships in `en.json` and `ar.json` together: the page title (المكوّنات), the group
names, the three hold choices (حتى أشغّله، لمدة ساعة، حتى الساعة 6 صباحًا غدًا), the dialog
sentences with plural forms (the existing `i18n.tsx` plural rules), and the drawer labels.
The component titles and descriptions (`components.<id>.title` / `.desc`), the phases
(`components.phase.*`: يعمل، متوقف، في الانتظار…) and the reasons (`components.reason.*`) are
51.01's words. The page is checked in Arabic: right-to-left layout (the switch sits at the
start edge), numbers and sizes formatted for `ar`, and no chip text cut off.

### 7. Help

A help topic `components` in `web/src/help/content` (en + ar) explains enable vs stop, holds,
waiting, and that nothing queued is lost. The page title row links it (`TitleRow`), and
`help.coverage.test.ts` checks it exists.

## Acceptance criteria

- **Given** an admin on `/settings/components`,
  **when** the page loads,
  **then** every catalog component is in its group with its phase and reason in words, and
  `core.*` rows have no switch.
- **Given** `llm` enabled and `classify.llm` enabled,
  **when** the admin turns `llm` off,
  **then** a dialog names Smart grouping and the parked job count before anything changes, and
  Cancel changes nothing.
- **Given** the admin confirms,
  **when** the change is applied,
  **then** the row moves through Draining to Off within 30 s on screen, and `classify.llm` shows
  Waiting with "needs LLM".
- **Given** the admin picks *Until tomorrow 06:00* on Transcription,
  **when** the hold is set,
  **then** the row shows "Stopped until 06:00" and a Start button.
- **Given** two admins with the page open,
  **when** both change the same row,
  **then** the second gets "Someone else changed this" and the row's real state.
- **Given** the language is Arabic,
  **when** the page renders,
  **then** every text is Arabic, the layout is right to left, and axe finds no violation.
- **Given** a viewer,
  **when** they open `/settings/components`,
  **then** there is no Components tab and the page shows "admins only".
- **Given** the Desktop app,
  **when** the owner picks **Open Components**,
  **then** the browser opens the page on the local home.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | vitest | fake API, 30 components | render | groups in order; chips with words; no switch on core |
| T02 | vitest | preview returns dependents | toggle `llm` off | dialog lists them; Cancel sends no PUT |
| T03 | vitest | preview returns nothing to stop | toggle `analytics` off | PUT sent at once; undo toast |
| T04 | vitest | preview returns `component_required_by` | remove runtime | refusal text, links, no confirm button |
| T05 | vitest | conflicts | enable `livetv.inprocess` | dialog names TV slots; PUT has `with_conflicts` |
| T06 | vitest | hold menu, fake clock 22:00 home time | pick "Until tomorrow 06:00" | stop body `until` = next day 06:00 home offset |
| T07 | vitest | 409 `stale_generation` | toggle | reload message; row updated |
| T08 | vitest | locale `ar` | render | `dir=rtl`; no English left (key-coverage check) |
| T09 | vitest | axe | render en and ar | no violations (`web/src/test/axe.ts`) |
| T10 | vitest | viewer | open route | no tab; admins-only panel |
| T11 | vitest | `server.toml` key differs | render LLM row | "the database wins" note with the key |
| T12 | vitest | hidden tab | advance 30 s | no poll while hidden |
| T13 | unit (Rust) | desktop `tray.rs` | `window_label(Ar, "open-components")` | Arabic label; opens `/settings/components` |
| T14 | integration (CI) | `components-live` job, real API + web build | flip a loop component through the page's calls | phase reaches Running ≤ 5 s (N2) |

## Edge cases

- **The API is down** while the page is open: the last answer stays, greyed, with the
  existing "can't reach your home" line. No switch works until it answers.
- **A component in Installing** shows a progress bar and no switch until it finishes (51.16).
- **Hundreds of model rows** (`model.llm.*`) are folded under "LLM models (12)".
- **Time zones.** "Until tomorrow 06:00" is the home's 06:00, not the browser's; the dialog says
  which when they differ.
- **Player and Remote web editions** (`VITE_MAKTABA_EDITION`) have no server pages; the tab is
  not built into them (Epic 42).

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/src/pages/Settings/ComponentsSection.tsx` (new) | The page. |
| `web/src/pages/Settings/ComponentDrawer.tsx` (new) | The ⋯ drawer. |
| `web/src/pages/Settings/ComponentConfirm.tsx` (new) | Preview dialog (design-system `Modal`). |
| `web/src/pages/Settings/components.css` (new) | Layout, chip colours from tokens, RTL. |
| `web/src/pages/Settings/ComponentsSection.test.tsx` (new) | T01–T12. |
| `web/src/lib/componentsApi.ts` (new) | Typed calls for 51.08's routes. |
| `web/src/pages/Settings.tsx` | The tab and route, admins only. |
| `web/src/i18n/en.json`, `web/src/i18n/ar.json` | Every string of §6. |
| `web/src/help/content/` (new topic) and `web/src/help/topics.ts` | The `components` help topic, en + ar. |
| `apps/desktop/src-tauri/src/tray.rs` | `open-components` item in the menu and `WINDOW_ITEMS`. |
| `apps/desktop/src-tauri/src/lang.rs` | Its English and Arabic label. |
| `apps/desktop/src-tauri/src/shell.rs` | Handle `open-components` beside `open-logs` (`shell.rs:1108`): open `<web_url>/settings/components`. |

### Tests

- T01–T12 in the web unit job (`make test-unit-web`). There is no Playwright in the repo;
  page tests use `web/src/test/pageHarness.tsx` and axe, as every other page.
- T13 in the desktop Rust tests.
- **CI only:** T14 in `components-live`. Screenshots for review are taken in dark theme.

### Rollout

Ships with Phase 2. Until it ships, the CLI (51.08) is the way to switch.

### Rollback

Hide the tab (one line in `Settings.tsx`); the API and the CLI keep working.

### Dependencies

51.08 (routes and preview), 51.01 (titles, groups, reason words).
