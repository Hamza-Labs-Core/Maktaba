# Story 54.18 — Store browser, listing pages, ratings and reports

> Epic 54 · Phase 2 · Depends on: 54.13, 54.17 · Needed by: 54.19

## Why

Owners find plugins in two places: inside their home (where they install) and on the web
(where they read about them, link to them, and rate them). Both show the same catalog, filtered
to what can run. Ratings and reports close the loop: they tell other owners what works, and
tell Maktaba what to look at first.

## Description

### 1. Browse, inside the home

Settings → Plugins → **Browse** (admin only):

- data from the home's cached catalog (54.17), filtered to the server version, plugin API and
  platform; incompatible versions hidden, with a toggle "Show plugins that need a newer Maktaba";
- search (name, summary, tags; Arabic-aware with the existing normaliser), categories, sort by
  rating, installs, newest;
- a plugin's page: icon, names, summary and description (en/ar), developer and verified
  prefix, rating and reviews, capabilities in Maktaba's words, review level badge
  (Reviewed / Automated checks), rating 1–6 (54.08), versions and notes, size, price (Phase 3),
  **Install** (54.07 install with the store reference; grants dialog);
- N13: ≤ 300 ms p95 from cache.

### 2. Listing pages on maktaba.tv

`maktaba.tv/plugins` and `maktaba.tv/plugins/<id>`: the same data, server-rendered by the cloud
web app for search engines, in English and Arabic (`/ar/plugins/…`), with an **Open in my
Maktaba** button that deep-links to the home's Browse page (through the cloud for linked homes)
and a **Download file** button for offline install (54.10). No sign-in to read; sign-in to rate
or report.

### 3. Ratings

- 1–5 stars and optional text (≤ 2,000 chars, any language), one per Maktaba account per plugin,
  editable; only from an admin of a home that has had the plugin installed for 24 hours (the
  store checks `store_installs` via the home's signed `seen`, or the home's own request when the
  admin rates from inside the home).
- The developer may reply once per review. Reviews can be reported.
- Averages shown with counts; fewer than 5 ratings shows "Not enough ratings".
- Text is plain (no links, no HTML), the same rule as panels.

### 4. Reports

- From the home (plugin page → Report) or the listing: reason (malware, data misuse, does not
  work, misleading, offensive, copyright, other) and details; the home attaches the plugin id,
  version and nothing else (no library data, no logs unless the admin ticks "Attach this
  plugin's logs from the last 24 hours", which go through Epic 53.16's opt-in path).
- Malware and data misuse page the store on-call and are triaged ≤ 24 h; others ≤ 2 business
  days. Outcomes: no action, contact developer, `warn`, `delist`, `disable` (54.17), developer
  suspended. The reporter is told the outcome in general terms.

### 5. Roles

Home: Browse, install, rate and report are admin-only, role-tested. Cloud: reading is public;
rating needs an eligible account; moderation routes need `store_reviewer`; role tests for each.

## Acceptance criteria

- **Given** a home on 0.6.2,
  **when** Browse opens,
  **then** only plugins with a version whose range includes 0.6.2 show, each with its newest
  compatible version.
- **Given** an admin whose home installed P 2 hours ago,
  **when** they try to rate,
  **then** "You can rate after using it for a day"; after 24 hours it works.
- **Given** a report with reason malware,
  **when** it is filed,
  **then** the on-call is paged and it appears at the top of the moderation queue.
- **Given** a review containing `https://…`,
  **when** displayed,
  **then** it shows as plain text, not a link.
- **Given** the Arabic listing page,
  **when** rendered,
  **then** it is RTL with the plugin's Arabic strings and Maktaba's Arabic capability words.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (web) | catalog fixture, server versions | filter | compatibility rules |
| T02 | unit (web) | search Arabic with/without diacritics | search | same results |
| T03 | integration (PG, go, cloud) | rating eligibility | rate early / later | 403 / 200 |
| T04 | integration (cloud) | malware report | file | page + queue top |
| T05 | unit (web, cloud web) | review with URL, HTML | render | plain text |
| T06 | role tests (home + cloud) | all routes | each role | per §5 |
| T07 | perf (web) | Browse from cache | open | ≤ 300 ms p95 (N13) |
| T08 | unit | en + ar | new strings | complete; RTL snapshots (dark theme) |

## Edge cases

- **Catalog older than 7 days** (expired): Browse shows the cached list with a banner and
  disables Install until refreshed.
- **A plugin delisted while its page is open**: Install answers `409 plugin_delisted`.
- **Ratings brigading**: rate limits per account and per home; reviewers can hide reviews.

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/src/pages/Admin/Plugins/Browse.tsx`, `StorePlugin.tsx` (new) | §1. |
| Cloud web app: `plugins` pages (new) | §2, en + ar. |
| `cloud/internal/store/ratings.go`, `reports.go` (new) | §3, §4. |
| `api/internal/handlers/plugins/report.go` (new) | Home-side report and rate proxy (admin only). |
| `web/src/i18n/{en,ar}.json`, cloud web strings | Strings. |

### Tests

- Unit T01, T02, T05, T08; integration T03, T04; role T06; perf T07.

### Rollout

Browse appears when the first catalog with listed plugins is fetched; before that the tab says
"The plugin store is opening soon".

### Rollback

Hide Browse with the store admin switch (an empty catalog); installs from files still work.

### Dependencies

54.13, 54.17.
