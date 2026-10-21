# Epic 41 — Help: a user guide and FAQ for every feature, in the app

> **Status:** spec. **Source:** `specs/epics/41-help-guide/`.
> **Anchors:** [Epic 11 — web UI](../11-web-ui/README.md) (routes, i18n, RTL),
> [Epic 17 — UX and design system](../17-ux-design-system/README.md), [Epic 06 — job queue](../06-job-queue/README.md)
> (stages, `error_cause`, plan 06-11), the rules schema (`api/internal/handlers/rules/schema.json`),
> the queue-health epic (in flight: "Failing items" page and failure classes).

## Goal

The owner's ask (2026-10-21): *"a user guide / FAQ for each feature: what to do, what it means; audit
the features and build the help, and have it linkable."*

1. **One help topic per feature**, in English and Arabic, inside the app at `/help`.
2. Each topic answers, in plain short language: **what it is**, **how to use it**, **what its states
   and messages mean**, **FAQ**, and **when something goes wrong** (every error kind: what it means,
   whether Maktaba fixes it itself, what you can do).
3. **Linkable everywhere**: every page title has a `?` link to its topic, every rules field links to
   its section of the guide, queue tiles and failure causes link to their entries; every link is a
   plain URL you can share.
4. **Help can't fall behind**: a test fails when a page, a rules section, a queue stage or an error
   kind has no help entry, or when a link points at a topic or anchor that doesn't exist.

Out of scope: a public (signed-out) help site, video tutorials, help for the developer tooling.

## Audit — the features (from the code, `origin/main` 3fd44a97)

Sources read: the routes in `web/src/App.tsx` (44 routes), the nav in `web/src/components/AppShell.tsx`,
the 24 rules sections and 413 fields of `schema.json`, the 41 core stages of
`pipeline/src/maktaba_pipeline/engine/graph.py` plus the 7 extension stages (Plex: `plex_index`,
`plex_collection`, `plex_groups`, `plex_livetv`; dizqueTV: `dizquetv_show`, `dizquetv_channels`,
`dizquetv_audit`), the 14 named failure causes of `engine/causes.py` and their labels/actions in
`api/internal/handlers/queues/causes.go`, the 12 waiting reasons in `queues/types.go`, and the
3,278 strings of `web/src/i18n/en.json`.

Legend: **Where** = route (and nav label). **Strings** = the `en.json` prefixes holding its states and
messages. **Errors** = what can go wrong and is surfaced to the user.

| # | Feature → help topic | What it is for | Where | Settings | Strings (states, messages) | Errors and failure kinds | What a user does in common situations |
|---|---|---|---|---|---|---|---|
| 1 | Getting started → `getting-started` | sign in, register, first-run setup, find your way, language, theme, shortcuts, notifications | `/login`, `/register`, `/more`, first-run screens (`Setup/SetupWizard`), notifications bell | Settings → Appearance (language, theme, density) | `login.*`, `register.*`, `mustChange.*`, `setup.*`, `shortcuts.*`, `notify.*`, `pwa.*` | wrong password, registration closed, must change password, `setup.error.*` (Hugging Face token, gated model, disk full, Plex code expired, tools install failed) | sign in; change the forced password; switch to Arabic; press `?` for shortcuts |
| 2 | Library → `library` | browse the videos of each library; scan folders; drop files to add them | `/library`, `/library/:id` (Library) | rules → Detection, Tagging | `library.*` (scan states, sort, filters, view) | scan failed, folder unreachable (see `failures`) | scan now after adding files; filter; open a video |
| 3 | Series → `series` | shows, seasons, episodes found by detection; continue watching; accept suggestions | `/series`, `/series/:id` (Series) | rules → Detection, Grouping | `series.*` | missing episodes, wrong season (fix by rules or Find metadata) | continue an episode; accept the season order |
| 4 | Videos and the player → `videos` | a video's page: details, transcript, chapters, subtitles, display title, stats; the player | `/videos/:id`, `/videos/:id/watch` | Settings → Appearance (subtitle size) | `video.*`, `player.*`, `context.*` | not found, no transcript yet, playback errors | play/resume, turn on subtitles, jump to a chapter |
| 5 | Search and YouTube → `search` | search titles and what is said (transcripts), semantic passages, YouTube results | `/search` (Search) | — | `search.*` incl. `search.youtube.*` | no results, YouTube unavailable | search a phrase; open the passage at its time |
| 6 | Watching Live TV → `watch-tv` | the TV guide and the live player; What's on now; watching away from home | `/guide`, `/live/:number` (TV Guide) | — (admin: `tv*` rules) | `guide.*`, `live.*`, `live.remote.*`, `won.*` | channel unavailable, reconnecting, remote limits (budget, full, not allowed, blocked network) | tune a channel; return to live; watch remotely |
| 7 | Your account → `account` | profile, password, devices, sessions, API tokens, watch history, privacy | `/account`, `/account/devices`, `/account/sessions`, `/account/tokens`, `/account/history`, Settings → Sessions, Privacy | Settings → Privacy | `account.*`, `devices.*`, `sessions.*`, `tokens.*`, `history.*`, `settings.privacy.*` | wrong current password, token shown once | sign out a lost device; make a token; clear history |
| 8 | Settings → `settings` | the Settings page: general, appearance, privacy, AI models, about | `/settings/*` | itself | `settings.*`, `settings.models.*` | model download/LLM errors | change language; pick models |
| 9 | Rules → `rules` | the rules editor: every planner/tagging/TV knob, one section each | `/settings/rules` | the 24 sections of `schema.json` | `settings.rules.*`, `rules.*` | validation errors on save, staged changes | find a field; reset to default; save |
| 10 | Updates → `updates` | signed releases installed by themselves in the quiet window; channel, pin, defer, skip | Settings → About, the update banner | rules → Updates | `update.*`, `updates.*`, `notify.update.*` | update held, rolled back | update now; defer; read release notes |
| 11 | Processing → `processing` | the queue: what runs, what waits and why, run/pause/retry, pools, item history, reports | `/queue`, `/queue/:stage`, `/queue/items/:kind/:id`, `/queue/reports` (Processing) | pools, per-stage on/off, review mode | `queue.*` (state, jobstate, outcome, waiting, issues, run, pool, retrigger, item, reports) | waiting reasons (12), causes (see `failures`) | why is it waiting? retry a failed group; pause all |
| 12 | Processing steps → `stages` | one entry per stage: what it does, what feeds it, what it feeds | each queue tile, `/queue/:stage` | per stage | `queue.stage.*.desc` | per-stage notes | know what a stage is before retrying it |
| 13 | Failures → `failures` | one entry per failure kind (`error_cause`) | Needs attention (queue), item history, transcription failures; later the queue-health "Failing items" page | — | `queue.issues.*`, cause labels (`causes.go`) | `nas_timeout`, `nas_invalid_argument`, `nas_partial_read`, `file_missing`, `ffmpeg_decode`, `youtube_bot_check`, `youtube_unavailable`, `plex_unreachable`, `plex_auth`, `dizquetv_unreachable`, `readback_mismatch`, `no_handler`, `graph_violation`, `db_error`, `awaiting_review`, `stale_graph`, and the generated `kind:message` causes | retry; fix the share; sign in to YouTube; open settings |
| 14 | Transcription → `transcription` | whole-library transcription progress, rate, ETA, failures | `/transcription` (Transcription) | Settings → AI models, rules → Tagging (transcripts) | `transcription.*` | stale, failures by cause | see how long is left; retry failures |
| 15 | Finding metadata → `metadata` | Find metadata / Re-enrich: proposals to accept or dismiss, revert | `/admin/enrichment`, video page "Find metadata" | rules → Finding metadata, Display titles, Exact dates | `enrich.*`, `video.displayTitle.*` | low confidence, nothing found, daily cap | accept a proposal; revert; search manually |
| 16 | Users → `users` | users, roles, invitations, open registration, forced password change | `/admin/users` | registration toggle | `admin.users.*`, `register.*` | last admin, username taken | add a user; reset a password |
| 17 | Library access → `library-access` | who may see which library | `/admin/library-acl` | — | `acl.*` | — | give a user a library |
| 18 | Channel editor → `channels` | build and edit channels: rule, schedule grid, slots, filler, live, art, channel strip, preview | `/admin/channels` | rules → Channels, Channel strip, Maktaba TV | `channels.*` | `channels.err.*` (number taken, stale copy, enable needs a rule) | make a channel; edit a slot; preview |
| 19 | Filler pools → `filler` | the clips that air between programmes | `/admin/filler` | rules → Channels (Fillers) | `fillerPools.*`, `filler.*` | — | add a clip; see which channels use a pool |
| 20 | Live TV administration → `live-tv-admin` | tuner addresses (HDHomeRun, M3U, XMLTV), Plex Live TV setup, streaming machines, on air, output | `/admin/live-tv` (Live TV access) | rules → Maktaba TV, TV output, TV machines | `livetv.*`, `tv.cluster.*`, `health.tv*` | channel down, host offline, failover | add Maktaba to Plex; move a channel to another machine |
| 21 | TV changes (staging) → `tv-changes` | every TV change is prepared next to the air, checked, approved, swapped; rollback | `/admin/live-tv/changes`, `/admin/live-tv/changes/:id` | rules → TV staging | `tvchanges.*`, `notify.tv.*` | `tvchanges.err.*`, checks fail/warn, failed swap, rolled back | review the diff; approve; roll back |
| 22 | Watching away from home → `remote-tv` | Cloudflare Stream for remote viewers: who, how many, budget, storage | `/admin/live-tv` (remote panel), the player's Remote badge | rules → TV away from home | `cfremote.*`, `cfstream.*`, `tv.remote.*`, `live.remote.*` | budget used up, storage full, channels full, not set up, blocked network | set the budget; let a user watch remotely |
| 23 | Simulcast → `simulcast` | send channels on to YouTube, Facebook or any RTMP destination | `/admin/live-tv` (Simulcast) | rules → Simulcast | `simulcast.*` | `simulcast.reason.*`, `simulcast.error.*`, test failed | add a destination; start now; read why it stopped |
| 24 | Analytics → `analytics` | watch time, top videos, peak hours, devices | `/admin/analytics` | — | `analytics.*` | — | change the range; export |
| 25 | Audit log → `audit-log` | who changed what, when | `/admin/audit` | — | `audit.*` | — | filter by user or action |
| 26 | System health → `system-health` | services, processes, disk, caches, budgets, TV lines, version | `/admin/health` | — | `health.*` | down / degraded services | restart a process; see what is down |
| 27 | Machines (cluster) → `machines` | every machine of the cluster: status, stages, storage, path maps, canonical paths, relay, versions, pausing | `/admin/machines`, the paused page, cluster sign-in | rules → Machines, Canonical share paths, Extension relay | `machines.*`, `cluster.*`, `paused.*`, `clusterSignIn.*` | `machines.reason.*`, version ahead/behind, storage unreachable, revoked | add a machine; drain; fix a version mismatch |
| 28 | Logs → `logs` | live logs of every service, filter, export | `/admin/logs` | — | `logs.*` | — | filter errors; export for a bug report |
| 29 | Extensions → `extensions` | Plex, dizqueTV and Cloudflare: connect, check, switch on/off | `/admin/extensions` | per extension | `queue.ext.*`, `ext.check.*` | checks failed, token rejected, unreachable | connect Plex; run a check |
| 30 | Plex → `plex` | Plex collections, the Plex index, Plex Live TV (tuner and guide) | `/admin/extensions`, `/admin/live-tv` (Plex) | rules → Plex Live TV, Extension relay | `livetv.plex.*` | `plex_auth`, `plex_unreachable` | sign in to Plex; refresh the guide |
| 31 | dizqueTV → `dizquetv` | writing the planned channels into dizqueTV, the channel audit | `/admin/extensions` | rules → dizqueTV export, Channel audit | stage strings | `dizquetv_unreachable`, `readback_mismatch` | check the address; apply a review |
| 32 | Devices and cloud → `cloud` | all devices (admin), billing and licence, cloud status | `/admin/devices`, `/billing`, `/cloud/status` | — | `devices.*`, `billing.*`, `cloud.status.*` | licence expired, relay down | see the plan; remove a device |
| 33 | The Mac app → `mac-app` | Maktaba.app: install, first-run setup, the server at login, the menu bar, updates | the Mac app window and menu bar, `Setup/*` | — | `setup.*` | `setup.error.*`, server starting / not running | finish setup; pause the server; open logs |

Every route of `App.tsx` maps to one of these topics (table "Page → topic" in the registry, story 41.3).

## Help information architecture

- **Categories** on `/help`: *Start* (getting-started, mac-app), *Watch and find* (library, series,
  videos, search, watch-tv), *Your account* (account, settings, updates), *Processing* (processing,
  stages, failures, transcription, metadata), *Live TV* (channels, filler, live-tv-admin, tv-changes,
  remote-tv, simulcast), *Administration* (users, library-access, machines, system-health, logs,
  audit-log, analytics, cloud), *Connections* (extensions, plex, dizquetv), *Rules* (rules).
- **Every topic has the same five sections**, with the same anchors in both languages:

  | Section | Anchor |
  |---|---|
  | What it is | `#what-it-is` |
  | How to use it | `#how-to-use` |
  | What the states and messages mean | `#states` |
  | FAQ | `#faq` |
  | When something goes wrong | `#troubleshooting` |

  plus topic-specific anchors (one per stage in `stages`, one per cause in `failures`, one per rules
  section in `rules`, one per waiting reason in `processing`, …).
- **Stable anchors**: written explicitly in the source (`## What it is {#what-it-is}`), never derived
  from the heading text, so English and Arabic share one URL and renaming a heading never breaks a link.
  Anchors are lowercase kebab-case; codes map by replacing `_` with `-` (`nas_timeout` → `#nas-timeout`).

## URL scheme

| URL | Shows |
|---|---|
| `/help` | the index: search box and every topic by category |
| `/help?q=<words>` | search results (shareable) |
| `/help/<topic>` | a topic |
| `/help/<topic>#<anchor>` | a topic scrolled to a section, highlighted |

Examples: `/help/failures#nas-timeout`, `/help/stages#transcribe`, `/help/rules#tv-remote`,
`/help/processing#waiting-no-worker`. The language follows the reader's setting, so one link works
for English and Arabic readers.

## Storage — Markdown per topic per language, bundled

`web/src/help/content/<locale>/<topic>.md`, loaded with Vite's `import.meta.glob(…, { query: "?raw" })`
(one chunk per file, fetched when needed), rendered by the app's own safe Markdown renderer
(`web/src/lib/markdown.tsx`, which never sets HTML), extended with `{#anchor}` headings and in-app links.

Why Markdown and not JSON: the content is prose with lists and links; Markdown keeps it readable in
review, diffs line by line, and an Arabic file sits next to its English one with the same headings.
JSON would put every paragraph in an escaped string. Topic titles and one-line summaries live in the
i18n catalogs (`help.topic.<id>.title|summary`), so the index renders without loading every topic and
the existing catalog checks cover them. It versions with the code: a feature PR changes its help in
the same diff, and the coverage tests make it.

## Linking

- `helpLink(topic, anchor?)` → `/help/<topic>#<anchor>`; topic ids are a typed list.
- **Registry** `web/src/help/registry.ts`: page route → topic (and anchor), rules section → anchor,
  stage → anchor, cause → anchor, waiting reason → anchor, plus `failureClassHelp()` as the entry
  point for the queue-health epic's failure classes.
- **Page titles**: a small `?` link (`PageHelp`) next to each page's `<h1>`, resolved from the route.
- **Rules fields**: each field's help text ends with "Learn more" → its section's anchor in `rules`.
- **Queue**: each stage tile and stage page links to `stages#<stage>`; each Needs-attention group and
  each failed step in item history links to `failures#<cause>`; waiting reasons link to
  `processing#waiting-<reason>`; transcription failures link to their cause.
- Every link is a real `<a href>`: open in a new tab, copy, share.

## Coverage tests (help can't fall behind)

| Test | Where | Fails when |
|---|---|---|
| topics | `web/src/help/help.coverage.test.ts` | a topic lacks its English or Arabic file, the five standard anchors, its title/summary strings, or the two languages have different anchors |
| links | same | a registry entry or an in-content `/help/…` link points at a topic or anchor that does not exist |
| pages | same | a route in `App.tsx` has no topic in the registry |
| rules | same (reads `schema.json`) | a rules section has no anchor in `rules` |
| stages, causes | `pipeline/tests/test_help_coverage.py` | a stage of the graph (core and extensions) or a cause of `causes.py` has no anchor in `stages` / `failures` |
| API causes, waiting reasons | `api/internal/handlers/queues/help_coverage_test.go` | a cause in `causes.go` or a waiting reason in `types.go` has no anchor |

## Writing rules

Plain, short sentences. Say what to do, not how it works inside. Name buttons and pages exactly as
the app shows them. Each error entry answers three things: what it means, whether Maktaba fixes it
by itself (it retries, it waits), and what you can do. Arabic is written as Arabic (natural phrasing,
the app's Arabic terms from `ar.json`), not translated word for word.

## Stories

| # | Story | Scope |
|---|---|---|
| 41.1 | **This spec** | the audit, the information architecture, the URL scheme, the stories |
| 41.2 | **Help area** | `/help` index with search (both languages, Arabic letter forms and diacritics folded), topic pages with a contents list, anchors that scroll and highlight, a copy-link button per section, phone layout, RTL; "Help" in the nav |
| 41.3 | **Links and registry** | `helpLink`, the registry, `PageHelp` next to every page title, rules field links, queue tile / cause / waiting reason / item history / transcription links |
| 41.4 | **Coverage tests** | the six tests above, in the web, pipeline and API suites |
| 41.5 | **Content** | the 33 topics in English and Arabic |
| 41.6 | **Queue-health classes** | link the queue-health epic's failure classes ("Failing items") through `failureClassHelp()` once it is on main |

## Acceptance

- `/help` lists every topic in the reader's language; search finds a word in either language.
- `/help/failures#nas-timeout` opens the entry, scrolled and highlighted, on a phone and in Arabic.
- Every page title, rules field, queue tile and failure cause has a working help link.
- Removing an anchor, adding a stage, a cause, a rules section or a route without help fails CI.
