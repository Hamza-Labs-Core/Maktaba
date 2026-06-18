# Story 28.6 — Update UI

> Epic 28 · Auto-Update · Phase 6 (UX)

## Description

One coherent update experience across web, desktop and mobile, built on
the same SPA (`web/dist`), so every client shows version, check status,
availability, release notes, and — where the platform allows — an action.

- **Settings → About/Updates section.**
  - Current version, commit (short), build date (from
    `GET /api/system/version` / `version.json`).
  - Update channel selector (stable / beta) — admin-editable; persists to
    server settings.
  - Last check time + a **"Check now"** button (calls
    `GET /api/system/updates?refresh=true`).
  - When an update is available: a card with the new version, release
    notes (rendered markdown), and a "View release" link.
- **Admin one-click update (server, web/desktop only).**
  - A **"Update now"** button (admin-only) that `POST`s
    `/api/admin/system/update {"confirm":true}` and shows progress
    (downloading → verifying → swapping → restarting), then a
    success/rolled-back result.
  - Docker/deb installs show the instruction returned by the 409 instead
    of a button that can't work.
- **Notification badge.** When an update is available, a badge on the
  settings nav icon (and the Settings → About link) so the user notices
  without opening Settings.
- **Mobile.** Reuses 28.5's banner for the "go to store" action; the
  Settings section shows version + channel but no server self-update
  button (a phone doesn't update the server).
- **i18n.** All strings in EN + AR, RTL-aware.

## Acceptance criteria

- **Given** any signed-in user opens Settings → About,
  **when** the page loads,
  **then** it shows current version, commit, build date, channel, and
  last-check time.

- **Given** an update is available,
  **when** the user opens Settings,
  **then** a badge is visible on the settings icon and the About section
  shows the new version + rendered release notes + a "View release" link.

- **Given** an **admin** on a plain-binary server with an update
  available,
  **when** they click "Update now" and confirm,
  **then** progress states render and, on success, the UI shows the new
  version after the server restarts (re-fetch `/api/system/version`).

- **Given** a **non-admin**,
  **when** they open Settings → About,
  **then** they see version info and "update available" but **no**
  "Update now" button.

- **Given** a Docker server with an update available,
  **when** an admin opens the section,
  **then** the `docker compose pull` instruction is shown instead of the
  update button.

- **Given** the user switches channel stable→beta,
  **when** they save,
  **then** the next check uses the beta channel and the available-version
  recomputes.

- **Given** Arabic locale,
  **when** the section renders,
  **then** all strings are translated and laid out RTL.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | component   | version endpoint stub | render About | fields shown |
| T02 | component   | updates available stub | render | new-version card + notes |
| T03 | component   | admin + available + binary | render | "Update now" visible |
| T04 | component   | non-admin | render | no "Update now" |
| T05 | component   | docker 409 | render | instruction shown, no button |
| T06 | component   | available | render nav | badge present |
| T07 | a11y        | About section | axe | no violations; badge has label |
| T08 | i18n        | ar locale | render | translated + `dir=rtl` |
| T09 | component   | "Check now" | click | calls `?refresh=true`, updates `checked_at` |

## Edge cases

- **Update check disabled.** Section shows "automatic checks are off"
  with the "Check now" button still available.
- **Network error on check.** Inline error, retry; doesn't blank the
  version display.
- **Update in progress, page refresh.** The button reflects "in
  progress" (server mutex → 409) rather than starting a second update.
- **Server restarts mid-update.** The web client polls
  `/api/system/version` until it answers again, then shows the new
  version (or surfaces the rolled-back state).
- **Release notes markdown.** Rendered with the existing safe markdown
  path (no raw HTML injection).
- **Long release notes.** Collapsed with "show more".

## Files / packages

- `web/src/pages/Settings.tsx` — replace the stub `AboutTab` with the
  full version/update section.
- `web/src/components/UpdateBanner.tsx`, `web/src/lib/update.ts` (shared
  with 28.5).
- `web/src/i18n/en.json`, `web/src/i18n/ar.json` — update keys.

## Open questions

- **Show per-service versions** (api/streaming/pipeline) for split
  deployments? The unified server reports one version; a split-deploy
  detail view is deferred.
