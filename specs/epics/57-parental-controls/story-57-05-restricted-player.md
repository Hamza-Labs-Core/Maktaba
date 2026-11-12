# Story 57.05 — The restricted Player on iPhone and iPad

> Epic 57 · Phase 3 (first) · Depends on: 57.03, 57.04 · Needed by: 57.06, 57.07, 57.11
> **Status:** built (checked against the code 2026-11-12) on iPhone and iPad. The child's side (the blocked page, Ask a
> parent, the parent PIN) is also built on the web, the Android Player (phone and tablet),
> Android TV and the desktop Player.

## Why

The home already hides what a child may not see (57.03). The Player must make that feel
deliberate, not broken. A link to a blocked video should say so and offer Ask a parent, not show
"Something went wrong". Controls a child cannot use, such as YouTube search or admin items,
should not show. Everything must work in Arabic, mirrored.

## Description

### 1. Knowing it is restricted

`GET /api/capabilities` gains `parental: {enabled, restricted, tier_limit, can_manage}` (see
README §6.6); the route answers 503 `parental_unavailable` when the limits cannot be read, never
a block that says nothing limits the account. The limit itself, with its `version` and what the
device said (`device_limit`, `device_pending`), is `GET /api/me/parental`, which the Player asks
at sign-in, at each return to the front and every 5 minutes. A change of the limit clears the
Player's per-account caches (Home shelves, the pictures in memory, the queue window), so nothing
hidden lingers on screen.

### 2. What changes on screen for a restricted sign-in

| Place | Change |
|---|---|
| Home, Libraries, Series, Collections, Playlists | nothing special: the home already sends only allowed items |
| Search | no YouTube results section and no Import from YouTube |
| Video page opened by a link, Continue, or a stale queue item that answers 404 | the **blocked page**: "This isn't available", the reason line if the home's `/api/me/parental` says the account or device is limited, **Ask a parent** and **Enter parent PIN** (57.06) |
| Player, during play, when opening the session again or a progress post is refused | playback stops, position saved, the blocked page; nothing is tried again |
| Profile | "Managed by Huda" and the limit (`parental.tier.*`), Age range row (57.04), no admin items |
| Live TV | only allowed channels (57.10); a channel that becomes blocked mid-programme shows the blocked slate |
| Plugin panels | only from allowed plugins (the home decides) |
| AirPlay | unchanged (the receiver plays the same signed URL) |

A device-limited sign-in on an unrestricted account (57.04) shows the same blocked page, with
"This device is limited to 9+" as the reason, and Enter parent PIN only if the account has
guardians.

### 3. The blocked page

```
┌───────────────────────────────────────┐
│               🔒                       │
│      This isn't available             │
│  Your account is limited to 9+.       │
│                                       │
│      [ Ask a parent ]                 │
│      [ Enter parent PIN ]             │
│              Back                     │
└───────────────────────────────────────┘
```

It never shows the title, poster or rating of what was asked for (the home does not send them).
Ask a parent sends the ID the child already had.

### 4. Arabic and accessibility

- Every string in the Player's catalogs with Arabic; mirrored layout checked in the UI tests in
  Arabic; tier labels carry a left-to-right mark (`‎+13`) so the plus sign sits correctly.
- VoiceOver reads the lock as "Not available"; buttons are at least 44 pt; Dynamic Type to AX5.

## Acceptance criteria

- **Given** a restricted child, **when** they open a shared link to a blocked video, **then** the
  blocked page shows, with no title or poster, in the app's language.
- **Given** a restricted child, **then** Search has no YouTube section, and Profile has no admin
  items.
- **Given** a guardian loosens the limit, **when** the Player returns to the foreground, **then**
  the capabilities version changes, caches clear, and the newly allowed items appear.
- **Given** a video playing when it is blocked, **then** at the next renewal playback stops, the
  position is saved, and the blocked page shows.
- **Given** Arabic, **then** every screen above is mirrored and every string is Arabic.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | MaktabaKit XCTest | capabilities with `parental` | decode | fields; version change clears caches |
| T02 | MaktabaKit XCTest | 404 on a video by link while restricted | open | blocked page model, not an error |
| T03 | Player UI test (iPhone sim, en) | `MockHome` restricted fixture | Home, Search, link to blocked, Profile | as acceptance |
| T04 | Player UI test (iPad sim, ar) | same | same | mirrored; Arabic strings |
| T05 | Player UI test (iPhone sim) | renewal refused mid-play | play | stops, saves, blocked page |
| T06 | catalog check | `--stringsdata` | build | every key has Arabic |

## Strings

| Key | English | Arabic |
|---|---|---|
| `parental.blocked.title` | This isn't available | هذا غير متاح |
| `parental.blocked.account` | Your account is limited to %@. | حسابك مقيَّد بـ %@. |
| `parental.blocked.device` | This device is limited to %@. | هذا الجهاز مقيَّد بـ %@. |
| `parental.blocked.ask` | Ask a parent | اطلب من أحد الوالدين |
| `parental.blocked.pin` | Enter parent PIN | أدخل رمز الوالدين |
| `parental.blocked.back` | Back | رجوع |
| `parental.blocked.a11y` | Not available | غير متاح |
| `parental.profile.managedBy` | Managed by %@ | يديره %@ |
| `parental.profile.limit` | Content limit | حدّ المحتوى |
| `parental.player.stopped` | This video is no longer available on this account. | لم يعد هذا المقطع متاحًا على هذا الحساب. |

## Edge cases

- **The home is older than Epic 57**: no `parental` block; the Player behaves as today.
- **A restricted child on a cloud sign-in**: identical (the home filters the same).
- **Offline**: no change; nothing new loads without the home.

## Build plan

| File | Change |
|---|---|
| `apps/apple/MaktabaKit/Sources/MaktabaKit/Capabilities.swift` | `parental` block |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/BlockedPage.swift` (new) | the page |
| `apps/apple/Player/Video/`, `Player/Watch/`, `Player/Search/`, `Player/Profile/` | wire it |
| `apps/apple/Player/*/…xcstrings`, `MaktabaKit/Localizable.xcstrings` | strings |
| `apps/apple/UITests/MockHome.swift`, `Fixtures/` | restricted fixtures |
