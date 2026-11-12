# Epic 57 — Parental controls: the home's rules on every device, with Apple's Screen Time

> **Status:** mostly built (checked against the code 2026-11-12): 57.01–57.10, 57.13–57.15, 57.17 and the home's and the web's side of 57.12 are built; 57.11 (Apple TV) and the iPhone notice of 57.12 are in progress; 57.16 is not built. **Source:** [`specs/epics/57-parental-controls/`](../../../specs/epics/57-parental-controls/README.md).
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

Two things go beyond a rating filter. Most content has no rating, so the home detects a tier
from each video's transcript. And children watch on more than Apple devices, so the home keeps
time limits and time slots of its own and enforces them on every client, with Screen Time
beside them on Apple devices.

## Key decisions

| | |
|---|---|
| Time | the home keeps daily limits and time slots, per account and per library, on its own clock, and enforces them on every client; Screen Time stays in use on Apple devices and carries the home's rules if Apple grants Family Controls |
| Detection | most content has no rating, so a pipeline stage detects a tier from the transcript on the home's own model; it counts only above a confidence line, never loosens, never beats a person; the rest goes to a review list |
| Accounts | a child has their own restricted account; admins and named guardians manage it; the API enforces it, with role tests |
| Ratings | one scale (All ages, 9+, 13+, 16+, 18+, unrated) from admin tiers, containers, Plex, guide, YouTube `age_limit` and plugins; region tables shared by Go and Swift; content rounds up, limits round down |
| Filter | one `parental` package used by every content route; a registry (`parental.Surfaces`) names every route of the API and a router walk fails on one it does not name; blocked answers 404, the same as missing |
| Apple's signals | Declared Age Range (gates 9, 13, 18) and `effectiveMaximumMovieRating` / `…TVShowRating`; they only tighten, stored per sign-in as a ratchet |
| Entitlements | `declared-age-range`; Family Controls only for 57.16, after a feasibility check, and nothing else depends on it |
| Unrated | the normal case; detection and library defaults shrink it, and what is left stays hidden from restricted accounts |
| Ask a parent | requests to guardians' Players or the web, or a guardian's 6-digit PIN checked on the home with lockouts; also for more time |
| Privacy | age band only, activity 90 days on the home, title-free notifications off by default |
| Migrations | 0220–0225 |
| Scope | as built: family settings on iPhone, iPad and the web (`/family`); the child's side (time, Ask a parent, the parent PIN, the blocked page, live TV) also on Android, Android TV and the desktop Player; Apple TV where it falls out; the filter and time rules on every client. Guardian screens on Android and the desktop Player are a later epic |

## Stories

| # | Story | Phase | Status |
|---|---|---|---|
| 57.01 | [Age tiers on the home's content](../../../specs/epics/57-parental-controls/story-57-01-age-tiers.md) | 1 | built |
| 57.02 | [Restricted accounts and guardians](../../../specs/epics/57-parental-controls/story-57-02-restricted-accounts.md) | 1 | built |
| 57.03 | [One content filter on every surface](../../../specs/epics/57-parental-controls/story-57-03-server-filter.md) | 1 | built |
| 57.04 | [The device's signal: Declared Age Range and the Screen Time rating limit](../../../specs/epics/57-parental-controls/story-57-04-device-signal.md) | 2, 3 | built |
| 57.05 | [The restricted Player on iPhone and iPad](../../../specs/epics/57-parental-controls/story-57-05-restricted-player.md) | 3 | built |
| 57.06 | [Ask a parent, and the parent PIN](../../../specs/epics/57-parental-controls/story-57-06-ask-a-parent.md) | 2, 3 | built |
| 57.07 | [Family settings for guardians in the Player](../../../specs/epics/57-parental-controls/story-57-07-family-settings.md) | 3 | built |
| 57.08 | [Activity for guardians](../../../specs/epics/57-parental-controls/story-57-08-activity.md) | 2, 3 | built |
| 57.09 | [Screen Time, Family Sharing and the App Store](../../../specs/epics/57-parental-controls/story-57-09-screen-time.md) | 3 | built |
| 57.10 | [Live TV under parental controls](../../../specs/epics/57-parental-controls/story-57-10-live-tv.md) | 1 | built |
| 57.11 | [Apple TV, where it falls out](../../../specs/epics/57-parental-controls/story-57-11-apple-tv.md) | 3 | in progress |
| 57.12 | [Turning it on in existing homes](../../../specs/epics/57-parental-controls/story-57-12-existing-homes.md) | 4 | built but the iPhone notice |
| 57.13 | [OpenAPI, role tests and the bypass rehearsal](../../../specs/epics/57-parental-controls/story-57-13-openapi-roles-rehearsal.md) | 1, 4 | built |
| 57.14 | [Time limits and time slots, kept by the home](../../../specs/epics/57-parental-controls/story-57-14-time-rules.md) | 2b | built |
| 57.15 | [Time limits on every client](../../../specs/epics/57-parental-controls/story-57-15-time-on-clients.md) | 3b | built but Apple TV |
| 57.16 | [The home's time rules through Screen Time](../../../specs/epics/57-parental-controls/story-57-16-screen-time-rules.md) | 3b | not built |
| 57.17 | [Tiers detected from transcripts](../../../specs/epics/57-parental-controls/story-57-17-detected-tiers.md) | 2b | built |
