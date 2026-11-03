# Story 54.13 — The admin Plugins page

> Epic 54 · Phase 1 · Depends on: 54.08, 54.09, 54.10, 54.11 · Needed by: 54.18, 54.20

## Why

The owner should manage plugins without a terminal: see what is installed, what each may
touch, whether it is healthy, what it wrote, and turn it off or remove it in one place. This
page is the friendly face of 54.07–54.11; the Components page (Epic 51) keeps listing plugins
too, for admins who think in components.

## Description

### 1. Where

Web: **Settings → Plugins** (admin only; viewers never see the entry, and the API refuses them
anyway). Native admin surfaces (the Mac app's server window, the Remote apps' admin screens)
link to the web page in Phase 1; they show plugin health in their existing component lists.

### 2. Installed tab

A list of cards, one per plugin: icon, name (plugin's strings in the admin's language),
developer, version, badges (Unreviewed, Development, Update available, Incompatible), rating
(1–6, with a tooltip of why), phase and reason (Epic 51 words), jobs today / failed today.

The plugin's page:

| Section | Content |
|---|---|
| Overview | summary, version, review level, installed date, compatible range |
| Permissions | capabilities in Maktaba's words (en/ar), libraries, toggles for optional ones, "Change" → `PUT /grants` |
| Status | phase, conditions, counters and p95s (54.09), quota bars, last errors with links to logs |
| Settings | the generated form (54.11) |
| What it wrote | `plugin_writes` paged and filterable (tags, chapters, subtitles, fields), with Revert selected |
| Versions | current and previous, Roll back, Update (when the catalog has one, Phase 2) |
| Danger | Disable / Enable, Stop / Start, Remove, Remove and clear what it wrote (with counts) |

### 3. Install from a file

A button opening 54.10's dialog: drop a file, see the summary, capabilities, libraries (none
ticked: the admin ticks each, decided 2026-11-03), rating, badge, typed confirmation for unreviewed, "Install" or "Install and
turn on".

### 4. Policy

At the bottom: **Allow unreviewed plugins** (54.10), **Plugins at once** (the `plugins` pool
size), **Limits** (lower the default memory and time caps for all plugins).

### 5. Words and directions

All Maktaba strings in `en.json` and `ar.json` together; RTL checked with the existing RTL
tests; capability words come from `capabilities.toml` keys (Maktaba's strings, so a plugin
cannot word its own permission request).

## Acceptance criteria

- **Given** an admin with two plugins installed, one held by the breaker,
  **when** the page opens,
  **then** both cards show; the held one says "Stopped itself after repeated errors" with a
  Start button.
- **Given** the Arabic UI,
  **when** the permissions section shows,
  **then** each capability is in Maktaba's Arabic words, and the layout is RTL.
- **Given** a plugin that wrote tags to 50 videos,
  **when** the admin selects 10 in What it wrote and reverts,
  **then** those 10 are reverted (where unchanged since) and the list updates.
- **Given** a viewer,
  **when** they open `/settings/plugins` directly,
  **then** they see the standard not-allowed page, and every API call behind it is 403.
- **Given** a remove with purge,
  **when** confirmed,
  **then** the dialog showed the counts of queued jobs to drop and writes to revert before.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (web) | mocked API, each phase | render | cards, badges, words |
| T02 | unit (web) | ar locale | render | RTL; Arabic capability words |
| T03 | unit (web) | permissions change | toggle + save | PUT body correct |
| T04 | unit (web) | What it wrote | select + revert | POST body; refresh |
| T05 | unit (web) | viewer | route | not-allowed page |
| T06 | e2e (Playwright, `plugins-e2e`) | real home, sample plugin | install from file → enable → run → disable → remove | each step visible |
| T07 | unit | i18n completeness | new keys | en + ar |

## Edge cases

- **Plugin strings missing in the admin's language**: impossible for valid packages (both
  required); for a broken install, fall back to English and show the id.
- **Very long plugin names in Arabic**: truncated with full text on hover/focus.
- **Accessibility**: cards and toggles have labels; the rating is announced as "Rating 3 of 6".

## Build plan

### Files to touch

| File | Change |
|---|---|
| `web/src/pages/Admin/Plugins/` (new): `Plugins.tsx`, `PluginDetail.tsx`, `InstallFromFile.tsx`, `Policy.tsx`, `Writes.tsx` | §2–§4. |
| `web/src/serverRoutes.tsx` | Route `/settings/plugins` (admin). |
| `web/src/lib/api/plugins.ts` (new) | Client for 54.08 routes. |
| `web/src/i18n/{en,ar}.json` | Strings. |
| `web/src/help/` | A help page for Plugins (Epic 41), en + ar. |
| `tests/plugins/e2e/plugins_page.spec.ts` (new) | T06. |

### Tests

- Unit T01–T05, T07; e2e T06 in `plugins-e2e`. Screenshots for review in the dark theme.

### Rollout

The Plugins entry appears for admins in the release that ships Phase 1, with an empty state:
"No plugins yet. Install one from a file." (Phase 2 adds Browse.)

### Rollback

Release rollback removes the page.

### Dependencies

54.08, 54.09, 54.10, 54.11.
