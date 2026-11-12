# Story 57.11 — Apple TV, where it falls out

> Epic 57 · Phase 3 · Depends on: 57.03, 57.04, 57.05, 57.10 · Needed by: —
> **Status:** in progress (2026-11-12).

## Why

This epic is for iPhone and iPad. Apple TV is included only where it costs little. Two things
come for free. First, the home's filter (57.03, 57.10) applies to any client, so a child's account
signed in on the Apple TV is already limited. Second, the TV app shares MaktabaKit, where the
blocked page and the signal code live. Apple's APIs decide the rest: on tvOS 26 the Screen Time
movie and TV limit can be read (`ManagedSettingsStore.effectiveMaximum…Rating`, tvOS 26+), but
Declared Age Range does not exist on tvOS.

## Description

| Piece | On Apple TV |
|---|---|
| Server filter, live TV masking and slate | yes, unchanged (the home does it) |
| Rating limit signal | yes: `AgeSignal.swift` reads the effective movie and TV limits on tvOS and posts them (57.04); no age range |
| Blocked page | yes, MaktabaKit's `BlockedPage` with focusable buttons: **Ask a parent** only (a PIN on a TV remote is left out: slow to type, and a parent nearby can use their phone's Requests) |
| Family settings, Activity, Rate libraries | no (a guardian uses the Player on iPhone or iPad) |
| Profile "Managed by" line and limit | yes, in Settings |
| Screen Time on tvOS | the parent sets it in Settings on the Apple TV; Help (57.09) points to it |

No new tvOS entitlement: reading the effective limit needs none. The TV app's existing UI tests run
one restricted fixture to prove the blocked page and the masked guide row are focusable and
mirrored in Arabic.

## Acceptance criteria

- **Given** a restricted child signed in on the Apple TV, **then** Home, Library, Search and Live
  show only allowed items, with no TV-specific change on the home.
- **Given** the Apple TV's Screen Time TV limit at TV-PG, **then** the TV app posts it and its
  sign-in is limited to 9+.
- **Given** a blocked item, **then** the blocked page has Ask a parent, focus lands on it, and
  Back returns.
- **Given** Arabic, **then** the page and the masked guide row are mirrored.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | MaktabaKit XCTest (tvOS destination where CI builds it; else the macOS run with `#if os(tvOS)` logic unit-tested) | double with TV limit 400 | build signal | `tv_ceiling: 400`, no age range |
| T02 | TV UI test (throwaway Apple TV sim, en) | `MockHome` restricted fixture | open a blocked item | blocked page; focus on Ask a parent |
| T03 | TV UI test (ar) | same | same; Live | mirrored; masked row |

## Strings

Reuses 57.05's `parental.blocked.*` and 57.10's `live.parental.*` from MaktabaKit; the TV
catalog gains:

| Key | English | Arabic |
|---|---|---|
| `tv.parental.askOnPhone` | A parent can answer from Maktaba on their iPhone or iPad. | يمكن لأحد الوالدين الرد من Maktaba على iPhone أو iPad. |

## Edge cases

- **An Apple TV used by the whole family on an adult's account**: no age range on tvOS, so only
  the Screen Time limit (if the parent set one on the Apple TV) tightens it.

## Build plan

| File | Change |
|---|---|
| `apps/apple/MaktabaKit/Sources/MaktabaKit/AgeSignal.swift` | tvOS branch (limits only) |
| `apps/tv/tvos/MaktabaTV/Views/` (VideoPage, Live, Settings) | blocked page, managed line |
| `apps/tv/tvos/MaktabaTV/Localizable.xcstrings` | string |
| TV UI tests | T02, T03 |
