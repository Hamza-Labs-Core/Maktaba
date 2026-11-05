# Story 55.10 — Help, README and the release check

> Epic 55 · Phase 3 · Depends on: 55.03, 55.04, 55.05, 55.06, 55.07, 55.08, 55.09 · Needed by: —

## Why

The desktop Player's help page (`web/src/help/content/{en,ar}/desktop-player.md`) and its
README describe story 42.4: libraries on the left, a title search and a bare player. Once the
other stories land, both are wrong. This story rewrites them and checks the installers still
build and update as before.

## Description

### 1. Help page, English and Arabic

`web/src/help/content/en/desktop-player.md` and `ar/desktop-player.md`, in the help pages'
shape (What it is, How to use it, What the states and messages mean, FAQ, When something goes
wrong):

- **The sections**: Home, Libraries, Live TV, Search, Profile; Back and Alt+Left.
- **Watch**: resume, Play from start, Audio, Subtitles, Quality, Up Next, the key table from
  55.08.
- **Live TV**: Now, Guide, a channel, away from home through Maktaba Cloud.
- **Profile**: history and Remove, favorites, playlists, sessions, approve a TV or device,
  playback settings, language.
- **A feature that is off** on the home: why a viewer does not see it, and what an admin sees.
- New messages from 55.03–55.09 under "What the states and messages mean".
- The Arabic page says «الجهاز الرئيسي» for the home and follows the English section for
  section.

### 2. README

`apps/desktop-player/README.md`: what the Player has (the sections), the `:client` code it
shares (README §5), the subtitle and audio approach (README §6), the
`-Pmaktaba.capabilitiesGate` switch, and how to run the screen tests.

### 3. Release check

- `_desktop-player.yml` builds the msi, deb and rpm and checks their names (unchanged); the
  epic adds the gate-off test step (55.02 T07).
- `desktop-release.yml`'s player legs build with the tag's version and no gate property.
- The update path is unchanged: `latest-player.json`, the platform keys and the minisign check
  are not touched by this epic; `UpdatesTest` and `MinisignTest` stay as they are.

## Acceptance criteria

- **Given** the help page in English and Arabic, **when** it is read, **then** every section and
  key of the Player is described, and the Arabic has the same sections.
- **Given** the PR, **when** CI runs, **then** `desktop-player (linux)` and `desktop-player
  (windows)` pass with the installers named as before.
- **Given** the next tag, **when** `desktop-release.yml` runs, **then** the player legs publish
  the msi, deb and rpm and `latest-player.json` as before.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | web unit (help) | the help content checks | build | both languages present, same anchors |
| T02 | CI | `_desktop-player.yml` | run | installers built and named |
| T03 | CI | `desktop-release.yml` on a tag | run | player assets and feed published |

## Edge cases

- **An older Player** reading the new help page: the page says which version added the
  sections ("since 0.3.x"), filled in when the version is known.

## Build plan

| File | Change |
|---|---|
| `web/src/help/content/en/desktop-player.md`, `web/src/help/content/ar/desktop-player.md` | Rewritten. |
| `apps/desktop-player/README.md` | Rewritten. |
| `.github/workflows/_desktop-player.yml` | The gate-off step (with 55.02). |
