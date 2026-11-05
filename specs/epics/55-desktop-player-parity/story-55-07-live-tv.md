# Story 55.07 — Live TV: Now, Guide, the channel player

> Epic 55 · Phase 2 · Depends on: 55.01, 55.02 · Needed by: 55.10

## Why

Live TV (Epic 27, story 48.30) is a section on the Android and Apple Players: what is on now on
every channel, a guide by time, and a channel player that works at home and away from home. The
desktop Player has none of it. This story adds it, playing the channel with JavaFX through
`StreamProxy`.

## Description

### 1. Guide maths into `:client`

The guide's time logic in the Android `GuideTab.kt` (the window shown, blocks cut to it, the
now line, Earlier and Later steps, Today and Tomorrow) moves into `:client` as `core.live.GuideWindow`,
with tests. The Android Player draws its guide from it; so does the desktop.

### 2. The section (`desktop/live/LiveTvScreen.kt`)

- Two tabs: **Now** and **Guide**.
- **Now**: `LiveApi.now()` as rows: logo (`LiveApi.logo(id)`), "Channel {0}, {1}", the
  programme on now with "{0}% through", and "Next at {0}: {1}". Choosing a channel opens
  `Route.Channel(id)`.
- **Guide**: `LiveApi.guide(start, end)` as a grid: channels down the start side, time across
  (right to left in Arabic), the now line, blocks sized by length. Earlier and Later move the
  window by two hours; the wheel and Shift+wheel scroll it.
- Empty: "No channels" and "This home has no channel you can watch."; error: "Can't load Live
  TV" and Try again.
- **Gate**: the shell hides the section from a viewer when `live_tv` is off (55.02). For an
  admin, the section shows `FeatureOffNote("live_tv", centered = true)` instead of the tabs.

### 3. The channel screen (`desktop/live/ChannelScreen.kt`, `ChannelPlayback.kt`)

- `VideoSurface` with the channel's picture, a LIVE badge, the programme on now and next under
  it, and the channel's guide for the rest of the day (`LiveApi.channelGuide`).
- Controls below the picture: Pause / Play, **Back to live** after a pause, Previous channel /
  Next channel (also Page Up / Page Down and Up / Down), Close (Escape).
- `ChannelPlayback` (its own class; `Playback` stays for videos) opens the stream with
  `:client`'s `LiveSource`: at home `LiveStream.Lan` (the web port's HLS), away from home
  `LiveStream.Relay` (tune, a 202 asked again until live, a heartbeat that may bring a new
  token).
- **`StreamProxy`** gains two rules: the web port's live HLS paths get the token, as `/stream/`
  does; and for a relay stream, every playlist and segment request to the Cloudflare host has
  its token path segment replaced with the newest token (`rewriteToken`), so a token change
  never reopens the player. No other host or path ever gets the token.
- Failures show the Android sentences for `LiveFailure.code`, with Try again when it is
  retryable; the remote session ended shows its sentence and Close.
- **A viewer on a stale route** (Live TV went off while the channel was open): the next call
  answers `409 component_off`, and the screen shows "Live TV isn't available on this home."

### 4. Words

| Key | English | Arabic |
|---|---|---|
| `live.now` | Now | الآن |
| `live.guide` | Guide | الدليل |
| `live.none` | No channels | لا قنوات |
| `live.noneDetail` | This home has no channel you can watch. | لا توجد على الجهاز الرئيسي قناة يمكنك مشاهدتها. |
| `live.cantLoad` | Can't load Live TV | تعذّر تحميل البث المباشر |
| `live.nothingOn` | Nothing scheduled | لا شيء في الجدول |
| `live.next` | Next at {0}: {1} | التالي في {0}: {1} |
| `live.channel` | Channel {0}, {1} | القناة {0}، {1} |
| `live.progress` | {0}% through | مضى {0}% |
| `live.today` | Today | اليوم |
| `live.tomorrow` | Tomorrow | غدًا |
| `live.earlier` | Earlier | أبكر |
| `live.later` | Later | لاحقًا |
| `live.onNow` | On now | يُعرض الآن |
| `live.badge` | LIVE | مباشر |
| `live.backToLive` | Back to live | العودة إلى البث المباشر |
| `live.nextChannel` | Next channel | القناة التالية |
| `live.previousChannel` | Previous channel | القناة السابقة |
| `live.tuning` | Tuning… | جارٍ الضبط… |
| `live.cantPlay` | This channel can't play | تعذّر تشغيل هذه القناة |
| `live.sessionEnded` | The remote session ended. | انتهت جلسة المشاهدة عن بُعد. |
| `live.remoteUnavailable` | Watching away from home isn't available right now. | المشاهدة من خارج المنزل غير متاحة الآن. |
| `live.streamsFull` | You're already watching on as many devices as allowed. | تشاهد الآن على أقصى عدد مسموح به من الأجهزة. |
| `live.budget` | This home's remote viewing allowance is used up. | استُنفد رصيد المشاهدة عن بُعد على الجهاز الرئيسي. |
| `live.remoteTimeout` | The channel took too long to start. | استغرق بدء القناة وقتًا طويلًا. |
| `live.notAllowed` | You can't watch away from home. | لا يمكنك المشاهدة من خارج المنزل. |
| `live.noSuchChannel` | This channel isn't available. | هذه القناة غير متاحة. |

## Acceptance criteria

- **Given** a home with channels, **when** Live TV shows, **then** Now lists every channel the
  person may watch with what is on now and next.
- **Given** the Guide, **when** Later is chosen, **then** the window moves two hours and the now
  line stays where now is; in Arabic time runs right to left.
- **Given** a channel at home, **when** it opens, **then** it plays within the tuning time, and
  Next channel (or Page Down) moves to the next one.
- **Given** a channel away from home, **when** the heartbeat brings a new token, **then** the
  picture does not stop.
- **Given** `live_tv` off and an admin, **when** they open Live TV, **then** they read the off
  note and the Components button.
- **Given** the relay answers "streams full", **when** the channel opens, **then** the screen
  shows that sentence in the person's language.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | unit (`:client`) | `GuideWindow` | blocks across midnight, Earlier/Later | cut to the window; now line; Today/Tomorrow |
| T02 | unit | `LiveTvState`, MockWebServer | load Now and Guide | rows; empty and error states |
| T03 | unit | `ChannelPlayback` with a fake `RemoteTv` | 202 then live; heartbeat with a new token | one open; token updated, no reopen |
| T04 | unit | `ChannelPlayback`, each `LiveFailure` code | open | the matching sentence key; retry only when retryable |
| T05 | unit | `StreamProxy` | a Cloudflare playlist and segment with an old token | newest token in the path |
| T06 | unit | `StreamProxy` | a request to another host or a non-stream path | no token added |
| T07 | screen | admin with `live_tv` off, `en`/`ar` | compose | off note centred |
| T08 | unit | channel up/down | at the last channel | wraps to the first, as Android |

## Edge cases

- **A channel with no logo**: its number in a circle.
- **The guide answers slowly** while the person moves the window twice: the older answer is
  dropped.
- **The window is closed** while a remote session is open: the session is ended on the home.

## Build plan

| File | Change |
|---|---|
| `apps/android/client/src/main/kotlin/com/hamzalabs/maktaba/core/live/GuideWindow.kt` (new) and test | Shared guide maths. |
| `apps/android/player/.../live/GuideTab.kt` | Uses it. |
| `apps/desktop-player/src/main/kotlin/com/hamzalabs/maktaba/desktop/live/LiveTvScreen.kt`, `LiveTvState.kt`, `GuideGrid.kt`, `ChannelScreen.kt`, `ChannelPlayback.kt`, `ChannelLogo.kt` (new) | The section. |
| `.../desktop/StreamProxy.kt` | Live paths; relay token rewrite. |
| `.../desktop/live/LiveStrings.kt` | §4 words. |
| `apps/desktop-player/src/test/.../live/*Test.kt` (new), `StreamProxyTest.kt` | T02–T08. |
