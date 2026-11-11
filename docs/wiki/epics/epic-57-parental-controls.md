# Epic 57 — Parental controls on iPhone and iPad, on Apple's Screen Time

> **Status:** spec (2026-11-08). **Source:** [`specs/epics/57-parental-controls/`](../../../specs/epics/57-parental-controls/README.md).
> **Anchors:** [Epic 10](epic-10-auth-security.md) (sessions, signed URLs) · [Epic 48](../../../specs/epics/48-native-players/README.md) (the native Players) · [Epic 56](epic-56-tv-library-view.md) (the play queue) · [research](../../../specs/epics/57-parental-controls/research.md).

## Goal

A parent can hand a child an iPhone or iPad with Maktaba on it and know the child sees only what
the parent allows: everywhere content can appear, on the home network and through Maktaba Cloud,
with no way around it from any app.

Apple already covers the device: Family Sharing child accounts, Screen Time time limits,
downtime and app limits, the App Store age gate and Ask to Buy. From iOS 26 an app can also learn
the child's age range (Declared Age Range) and read the device's movie and TV rating limit.
Maktaba rates its own content and filters it on the home, for restricted accounts and for devices
that report a child.

## Key decisions

| | |
|---|---|
| Device side | Screen Time's: no Maktaba time limits, bedtime or app-leave PIN |
| Accounts | a child has their own restricted account; admins and named guardians manage it; the API enforces it, with role tests |
| Ratings | one scale (All ages, 9+, 13+, 16+, 18+, unrated) from admin tiers, containers, Plex, guide, YouTube `age_limit` and plugins; region tables shared by Go and Swift; content rounds up, limits round down |
| Filter | one `parental` package used by every content route; a registry walk fails on a route that skips it; blocked answers 404, the same as missing |
| Apple's signals | Declared Age Range (gates 9, 13, 18) and `effectiveMaximumMovieRating` / `…TVShowRating`; they only tighten, stored per sign-in as a ratchet |
| Entitlements | `declared-age-range` only; no Family Controls (it needs Apple's approval per extension, and reading the limit does not need it) |
| Unrated | hidden from restricted accounts by default; admins rate whole libraries in one step |
| Ask a parent | requests to guardians' Players, or a guardian's 6-digit PIN checked on the home with lockouts |
| Privacy | age band only, activity 90 days on the home, title-free notifications off by default |
| Migrations | 0220–0222 planned; taken at merge time per `MANIFEST.md` |
| Scope | iPhone and iPad; Apple TV where it falls out; Android, web and desktop a later epic (the home's filter already covers them) |

## Stories

| # | Story | Phase |
|---|---|---|
| 57.01 | [Age tiers on the home's content](../../../specs/epics/57-parental-controls/story-57-01-age-tiers.md) | 1 |
| 57.02 | [Restricted accounts and guardians](../../../specs/epics/57-parental-controls/story-57-02-restricted-accounts.md) | 1 |
| 57.03 | [One content filter on every surface](../../../specs/epics/57-parental-controls/story-57-03-server-filter.md) | 1 |
| 57.04 | [The device's signal: Declared Age Range and the Screen Time rating limit](../../../specs/epics/57-parental-controls/story-57-04-device-signal.md) | 2, 3 |
| 57.05 | [The restricted Player on iPhone and iPad](../../../specs/epics/57-parental-controls/story-57-05-restricted-player.md) | 3 |
| 57.06 | [Ask a parent, and the parent PIN](../../../specs/epics/57-parental-controls/story-57-06-ask-a-parent.md) | 2, 3 |
| 57.07 | [Family settings for guardians in the Player](../../../specs/epics/57-parental-controls/story-57-07-family-settings.md) | 3 |
| 57.08 | [Activity for guardians](../../../specs/epics/57-parental-controls/story-57-08-activity.md) | 2, 3 |
| 57.09 | [Screen Time, Family Sharing and the App Store](../../../specs/epics/57-parental-controls/story-57-09-screen-time.md) | 3 |
| 57.10 | [Live TV under parental controls](../../../specs/epics/57-parental-controls/story-57-10-live-tv.md) | 1 |
| 57.11 | [Apple TV, where it falls out](../../../specs/epics/57-parental-controls/story-57-11-apple-tv.md) | 3 |
| 57.12 | [Turning it on in existing homes](../../../specs/epics/57-parental-controls/story-57-12-existing-homes.md) | 4 |
| 57.13 | [OpenAPI, role tests and the bypass rehearsal](../../../specs/epics/57-parental-controls/story-57-13-openapi-roles-rehearsal.md) | 1, 4 |
