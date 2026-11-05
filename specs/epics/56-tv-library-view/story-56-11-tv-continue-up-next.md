# Story 56.11 — Continue and Up next on the TVs

> Epic 56 · Phase 3 · Depends on: 56.01, 56.02, 56.04 · Needed by: —

## Why

When a video ends on a TV, the player stops. The phone Players count down to the next episode
(`UpNext.swift`, `UpNext.kt`) and the web plays its queue's next video. Plex auto-plays the next
episode with a countdown and keeps On Deck and Continue Watching on Recommended and Home.

## Description

### 1. Up next

When fewer than 30 s remain (or the end credits chapter starts, when the video has chapters), an
**Up next** card appears at the bottom end corner: the next queue item's poster, title and series,
a 10 s countdown, **Play now** and **Cancel**. Focus moves to Play now without pausing the video;
Back or Cancel dismisses it and the player stops at the end. The countdown runs only when the
device's **Play the next video automatically** setting is on (default on, as the Players'); off,
the card shows without a countdown.

The next item is the queue's (56.01). A video started on its own (not from Play or Shuffle) still
makes a `video` queue, so an episode goes on to the rest of its series, as the Players do today.

### 2. Continue

- Recommended (56.03) and Home show **Continue Watching** (started, not finished) and **On Deck**
  (each series' next unwatched episode, `ondeck.go`).
- Home's first row offers **Continue your queue** when `GET /api/me/play-queues/current` returns a
  queue used in the last 24 h, from any device: "{title} · from {device}". Select resumes it at its
  current item and position.

### 3. Settings

A **Playback** section in each TV app's settings: **Play the next video automatically** (on),
kept on the device.

## Acceptance criteria

- **Given** a series queue on either TV, **when** an episode reaches its last 30 s, **then** the Up
  next card shows the next episode and, after 10 s past the end, plays it.
- **Given** the setting off, **then** the card shows with no countdown and playback stops at the
  end.
- **Given** a queue started on a phone 10 minutes ago, **when** the TV's Home opens, **then** its
  first row offers to continue it, and Select plays the same video at the phone's position.
- **Given** Arabic, **then** the card sits at the bottom left and its strings are Arabic.

## Test cases

| ID | Type | Platform | Setup | Action | Expected |
|---|---|---|---|---|---|
| T01 | unit (Kotlin) | Android TV | player model, fake clock | reach end − 30 s | card state, countdown, advance |
| T02 | Compose UI test | Android TV | setting off | reach end | no countdown; stop |
| T03 | Compose UI test | Android TV | `current` queue mock | Home | Continue your queue row |
| T04 | XCTest (MaktabaKit) | Apple | `PlayQueue` + fake clock | end | advance after 10 s |
| T05 | XCUITest (`XCUIRemote`) | Apple TV | `MockHome` short fixture video, queue of 2 | play to end | Up next card, then the second video's title |
| T06 | XCUITest | Apple TV | `current` queue fixture | Home | row shows; Select plays |
| T07 | XCUITest | Apple TV | Arabic | Up next | card at bottom left (frame) |
| T08 | Roborazzi | Android TV | card en/ar dark | render | screenshots |
| T09 | i18n | both | new keys | missing-Arabic tests | pass |

## Strings

| Key | English | Arabic |
|---|---|---|
| `player.upNext` | Up next | التالي |
| `player.upNext.in` | Up next in {n} s | التالي خلال {n} ث |
| `player.playNow` | Play now | شغّل الآن |
| `common.cancel` | Cancel | إلغاء |
| `home.continueQueue` | Continue your queue | تابع قائمة الانتظار |
| `home.continueQueue.from` | {title} · from {device} | {title} · من {device} |
| `settings.playback` | Playback | التشغيل |
| `settings.autoplayNext` | Play the next video automatically | شغّل المقطع التالي تلقائيًا |

## Edge cases

- **The last item of a finished source**: no card; the player returns to the page it came from.
- **A queue whose next item became unreadable**: skipped; the card shows the one after.
- **Live TV**: unchanged (no queue).

## Build plan

| File | Change |
|---|---|
| `apps/tv/android/.../ui/screens/PlayerScreen.kt`, `UpNextCard.kt` (new), `HomeScreen.kt`, settings | TV |
| `apps/tv/tvos/MaktabaTV/Views/PlayerView.swift`, `HomeView.swift`, `SettingsView.swift` | TV |
| `apps/apple/MaktabaKit/Sources/MaktabaKit/HomeShelves.swift` | the queue row |
| tests as in the table | |
